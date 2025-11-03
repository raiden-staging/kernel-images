# Audio Implementation Flow - Complete Verification

## Architecture Comparison

### Previous State (Working Audio)
- **Process Management**: Direct backgrounding with `&`
- **Xorg**: `/usr/bin/Xorg ... &`
- **D-Bus**: `dbus-daemon ... &` → save `dbus_pid=$!`
- **PulseAudio**: `pulseaudio ... &` → save `pulse_pid=$!`
- **Wait loops**: Directly in wrapper.sh

### Current State (Supervisord Architecture)
- **Process Management**: Supervisord manages all services
- **Xorg**: `supervisorctl start xorg`
- **D-Bus**: `supervisorctl start dbus`
- **PulseAudio**: `supervisorctl start pulseaudio` ← NEW
- **Chromium**: `supervisorctl start chromium`
- **Wait loops**: In wrapper.sh after supervisorctl start

## Complete PulseAudio Startup Flow

### Step-by-Step Execution

```
1. wrapper.sh:204
   ↓
   supervisorctl -c /etc/supervisor/supervisord.conf start pulseaudio

2. supervisord reads: supervisor/services/pulseaudio.conf
   ↓
   [program:pulseaudio]
   command=/bin/bash -lc '/images/chromium-headful/start-pulseaudio.sh'

3. supervisord executes: start-pulseaudio.sh
   ↓
   if RUN_AS_ROOT == "true" → exit 0

4. start-pulseaudio.sh:11-14 (STRICT - no error suppression)
   ↓
   chown -R kernel:kernel /home/kernel/ /home/kernel/.config /etc/pulse
   chmod 777 /home/kernel/.config /etc/pulse
   chown -R kernel:kernel /tmp/runtime-kernel

5. start-pulseaudio.sh:18-25
   ↓
   exec runuser -u kernel -- env \
     XDG_RUNTIME_DIR=/tmp/runtime-kernel \
     XDG_CONFIG_HOME=/home/kernel/.config \
     XDG_CACHE_HOME=/home/kernel/.cache \
     pulseaudio --log-level=error \
                --disallow-module-loading \
                --disallow-exit \
                --exit-idle-time=-1

   ↓ (exec replaces script process with pulseaudio)

   PulseAudio daemon now running as kernel user

6. PulseAudio reads configs:
   ↓
   /etc/pulse/daemon.conf (low-latency settings, enable-shm=no)
   /etc/pulse/default.pa (virtual sinks, modules)

7. wrapper.sh:206-216 (WAIT LOOP)
   ↓
   for i in $(seq 1 20); do
     runuser -u kernel -- pactl info >/dev/null 2>&1 → SUCCESS?
     ↓ YES
     echo "[wrapper] PulseAudio is ready"
     break
     ↓ NO (after 20 tries)
     echo "[wrapper] ERROR: PulseAudio failed to start"
     exit 1  ← FAIL HARD
   done

8. wrapper.sh:220
   ↓
   supervisorctl start chromium → Chromium can now use PulseAudio
```

## NO CONFLICTS - Verification Checklist

### ✅ No Duplicate Startups
- PulseAudio is started ONLY ONCE via `supervisorctl start pulseaudio` (wrapper.sh:204)
- No direct `pulseaudio &` commands in wrapper.sh
- No multiple supervisor services for pulseaudio

### ✅ Permission Setup Happens Once
- ONLY in start-pulseaudio.sh (lines 11-14)
- NOT in wrapper.sh
- Strict mode (no `2>/dev/null || true`)

### ✅ Wait Loop Happens Once
- ONLY in wrapper.sh (lines 206-216)
- NOT in start-pulseaudio.sh (uses `exec`)
- Fails hard if PulseAudio doesn't respond

### ✅ Process Lifecycle Managed by Supervisord
- supervisord starts: start-pulseaudio.sh
- start-pulseaudio.sh does: `exec pulseaudio` (becomes the supervised process)
- supervisord monitors the pulseaudio process
- wrapper.sh cleanup calls: `supervisorctl stop pulseaudio`

### ✅ Environment Variables Set Correctly
- wrapper.sh:8-12: Export audio env vars
- start-pulseaudio.sh:18-21: Passes env vars to pulseaudio process
- Dockerfile:256-257: Sets ENV for container

## Configuration Files (All Correct)

### /etc/pulse/daemon.conf
- Low-latency settings (5ms fragments)
- `enable-shm = no` (critical for containers)
- 48kHz sample rate

### /etc/pulse/default.pa
- Virtual audio sinks: audio_output, audio_input
- Virtual microphone source
- module-native-protocol-unix (creates socket)
- module-always-sink

### /etc/dbus-1/system.d/pulseaudio.conf
- Allows pulse/pulse-access/audio groups to own PulseAudio D-Bus services

### /etc/dbus-1/system.d/mpris.conf
- Allows all users to own MPRIS services (Chromium media control)

## Startup Order (Critical)

```
1. supervisord starts
2. wrapper.sh → supervisorctl start xorg
3. wrapper.sh → supervisorctl start mutter
4. wrapper.sh → supervisorctl start dbus
5. wrapper.sh → supervisorctl start pulseaudio  ← NEW
   └─ Wait for pactl info to succeed
6. wrapper.sh → supervisorctl start chromium
   └─ Chromium now has audio available
7. wrapper.sh → supervisorctl start neko
   └─ Neko can capture from pulsesrc device=audio_output.monitor
```

## Log Markers to Look For

When you rebuild and deploy, you should see this EXACT sequence:

```
[wrapper] Starting system D-Bus daemon via supervisord
[wrapper] Waiting for D-Bus system bus socket...
dbus: started
[wrapper] Starting PulseAudio daemon via supervisord
[pulseaudio] Setting up permissions
[pulseaudio] Starting daemon as kernel user
[wrapper] Waiting for PulseAudio server...
[wrapper] PulseAudio is ready
[wrapper] Starting Chromium via supervisord on internal port 9223
chromium: started
[wrapper] ✨ Starting neko (webrtc server) via supervisord.
neko: started
```

## If PulseAudio Fails

Expected error flow:

```
[wrapper] Starting PulseAudio daemon via supervisord
[pulseaudio] Setting up permissions
[pulseaudio] Starting daemon as kernel user
[wrapper] Waiting for PulseAudio server...
[wrapper] ERROR: PulseAudio failed to start
← Script exits with code 1, container stops
```

Look in `/var/log/supervisord/pulseaudio` for the actual error.

## Reference: Working Version Commands

From previous-state-with-audio-support wrapper.sh (lines 99-123):

```bash
echo "[pulse] Setting up permissions"
chown -R kernel:kernel /home/kernel/ /home/kernel/.config /etc/pulse
chmod 777 /home/kernel/.config /etc/pulse
chown -R kernel:kernel /tmp/runtime-kernel

echo "[pulse] Starting daemon"
runuser -u kernel -- env \
  XDG_RUNTIME_DIR=/tmp/runtime-kernel \
  XDG_CONFIG_HOME=/home/kernel/.config \
  XDG_CACHE_HOME=/home/kernel/.cache \
  pulseaudio --log-level=error \
             --disallow-module-loading \
             --disallow-exit \
             --exit-idle-time=-1 &
pulse_pid=$!

echo "[pulse] Waiting for server"
for i in $(seq 1 20); do
  runuser -u kernel -- pactl info >/dev/null 2>&1 && break
  if [ "$i" -eq 20 ]; then
    echo "[pulse] ERROR: failed to start"
    exit 1
  fi
  sleep 0.5
done
```

Our adaptation for supervisord architecture maintains the SAME behavior but splits it:
- **Permissions + Start**: start-pulseaudio.sh (exec mode for supervisord)
- **Wait loop**: wrapper.sh (same error handling)
