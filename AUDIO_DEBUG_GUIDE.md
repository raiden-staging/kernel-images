# Audio Implementation Debug Guide

## Current Status

Audio support has been implemented but PulseAudio is not starting. The logs show:
- NO PulseAudio initialization messages
- Chromium trying to use ALSA directly (and failing)
- Neko trying to capture from non-existent PulseAudio

## Diagnostic Logging Added

I've added comprehensive logging to `wrapper.sh` to diagnose the issue. When you rebuild and test, look for these markers:

### Expected Log Sequence:

```
[wrapper] ========== AUDIO SETUP START ==========
[wrapper] DEBUG: RUN_AS_ROOT='<value>' (expecting empty or 'false')
[wrapper] DEBUG: Checking if '<value>' != 'true'
[wrapper] ✓ Conditional passed - setting up PulseAudio as kernel user
[wrapper] Setting up PulseAudio permissions
[wrapper] Starting PulseAudio daemon as kernel user
[wrapper] Waiting for PulseAudio server
[wrapper] PulseAudio is ready
[wrapper] ========== AUDIO SETUP COMPLETE ==========
```

### If Conditional Fails (RUN_AS_ROOT=true):

```
[wrapper] ========== AUDIO SETUP START ==========
[wrapper] DEBUG: RUN_AS_ROOT='true'
[wrapper] DEBUG: Checking if 'true' != 'true'
[wrapper] ✗ Conditional failed - RUN_AS_ROOT=true, skipping PulseAudio setup
[wrapper] ========== AUDIO SETUP COMPLETE ==========
```

## What to Check in New Logs

1. **If you see AUDIO SETUP START banner:**
   - Check the value of RUN_AS_ROOT
   - See which branch executes (✓ or ✗)
   - If ✓ but no "PulseAudio is ready", check for error messages

2. **If you DON'T see AUDIO SETUP START banner:**
   - wrapper.sh is not the correct version
   - OR script is exiting early before line 208
   - OR there's a syntax error in bash

3. **If you see "ERROR: PulseAudio failed to start":**
   - PulseAudio daemon crashed immediately
   - Check permissions on /etc/pulse, /tmp/runtime-kernel
   - Verify kernel user exists and is in audio/pulse groups

## Files Modified

1. **Dockerfile** - Added audio packages, user groups, env vars, config file COPYs
2. **wrapper.sh** - Added PulseAudio initialization between D-Bus and Chromium
3. **xorg.conf** - Added SocketMode "0666" for kernel user socket access
4. **video.vue** - Added auto-unmute functionality with visual indicator
5. **start-pulseaudio.sh** - Updated to use config files properly

## New Files Created

1. **daemon.conf** - PulseAudio daemon config with low-latency settings
2. **default.pa** - PulseAudio startup script (virtual audio devices)
3. **dbus-pulseaudio.conf** - D-Bus permissions for PulseAudio
4. **dbus-mpris.conf** - D-Bus permissions for MPRIS (Chromium media control)

## Next Steps

1. **Rebuild the image** with the latest wrapper.sh changes
2. **Deploy and capture logs**
3. **Search logs for "AUDIO SETUP"** to see diagnostic output
4. **Share the relevant section** of logs showing the audio setup sequence

## Critical Configuration

### Environment Variables (must be set):
- `RUN_AS_ROOT=false` (or not set at all)
- `XDG_RUNTIME_DIR=/tmp/runtime-kernel`
- `PULSE_SERVER=unix:/tmp/runtime-kernel/pulse/native`

### User Configuration:
- kernel user must be in: audio, video, pulse, pulse-access groups

### Directory Permissions:
- /etc/pulse → chmod 777
- /home/kernel/.config → owned by kernel:kernel
- /tmp/runtime-kernel → owned by kernel:kernel

### Startup Order:
1. Xorg
2. Mutter
3. D-Bus
4. **PulseAudio** ← Should start here!
5. Chromium
6. Neko

## If PulseAudio Still Doesn't Start

Run these commands manually in the container to test:

```bash
# Check kernel user groups
id kernel

# Check directory permissions
ls -la /etc/pulse
ls -la /tmp/runtime-kernel
ls -la /home/kernel/.config

# Try starting PulseAudio manually
runuser -u kernel -- env \
  XDG_RUNTIME_DIR=/tmp/runtime-kernel \
  XDG_CONFIG_HOME=/home/kernel/.config \
  XDG_CACHE_HOME=/home/kernel/.cache \
  pulseaudio --log-level=debug --log-target=stderr

# Check if PulseAudio socket was created
ls -la /tmp/runtime-kernel/pulse/native

# Test with pactl
runuser -u kernel -- pactl info
```
