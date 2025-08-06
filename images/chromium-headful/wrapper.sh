#!/bin/bash

set -o pipefail -o errexit -o nounset

export PULSE_SERVER=unix:/tmp/pulseaudio.socket

# If the WITHDOCKER environment variable is not set, it means we are not running inside a Docker container.
# Docker manages /dev/shm itself, and attempting to mount or modify it can cause permission or device errors.
# However, in a unikernel container environment (non-Docker), we need to manually create and mount /dev/shm as a tmpfs
# to support shared memory operations.
if [ -z "${WITHDOCKER:-}" ]; then
  mkdir -p /dev/shm
  chmod 777 /dev/shm
  mount -t tmpfs tmpfs /dev/shm
fi

# We disable scale-to-zero for the lifetime of this script and restore
# the original setting on exit.
SCALE_TO_ZERO_FILE="/uk/libukp/scale_to_zero_disable"
scale_to_zero_write() {
  local char="$1"
  # Skip when not running inside Unikraft Cloud (control file absent)
  if [[ -e "$SCALE_TO_ZERO_FILE" ]]; then
    # Write the character, but do not fail the whole script if this errors out
    echo -n "$char" > "$SCALE_TO_ZERO_FILE" 2>/dev/null || \
      echo "Failed to write to scale-to-zero control file" >&2
  fi
}
disable_scale_to_zero() { scale_to_zero_write "+"; }
enable_scale_to_zero()  { scale_to_zero_write "-"; }

# Disable scale-to-zero for the duration of the script when not running under Docker
if [[ -z "${WITHDOCKER:-}" ]]; then
  echo "Disabling scale-to-zero"
  disable_scale_to_zero
fi

export DISPLAY=:1

/usr/bin/Xorg :1 -config /etc/neko/xorg.conf -noreset -nolisten tcp &

./mutter_startup.sh

# -----------------------------------------------------------------------------
# System-bus setup --------------------------------------------------------------
# -----------------------------------------------------------------------------
# Start a lightweight system D-Bus daemon if one is not already running.
# This must be done BEFORE pulseaudio, which depends on it.
if [ ! -S /run/dbus/system_bus_socket ]; then
  echo "Starting system D-Bus daemon"
  mkdir -p /run/dbus
  # Ensure a machine-id exists (required by dbus-daemon)
  dbus-uuidgen --ensure
  # Launch dbus-daemon in the background and remember its PID for cleanup
  dbus-daemon --system \
    --address=unix:path=/run/dbus/system_bus_socket \
    --nopidfile --nosyslog --nofork >/dev/null 2>&1 &
  dbus_pid=$!

  # Wait for the D-Bus socket to become available
  echo "Waiting for D-Bus socket..."
  for i in $(seq 1 20); do
    if [ -S /run/dbus/system_bus_socket ]; then
      break
    fi
    if [ $i -eq 20 ]; then
      echo "D-Bus socket not found after 10 seconds. Aborting." >&2
      exit 1
    fi
    sleep 0.5
  done
  echo "D-Bus socket is available."
fi

# We will point DBUS_SESSION_BUS_ADDRESS at the system bus socket to suppress
# autolaunch attempts that failed and spammed logs.
export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/dbus/system_bus_socket"

echo "Starting PulseAudio daemon..."
runuser -u kernel -- pulseaudio --log-level=error --disallow-module-loading --disallow-exit --exit-idle-time=-1 &
pulse_pid=$!

# Wait for pulseaudio socket to be available, with a timeout
echo "Waiting for PulseAudio socket..."
for i in $(seq 1 20); do
  if [ -S "$PULSE_SERVER" ]; then
    break
  fi
  if [ $i -eq 20 ]; then
    echo "PulseAudio socket not found after 10 seconds. Aborting." >&2
    exit 1
  fi
  sleep 0.5
done
echo "PulseAudio socket is available."

if [[ "${ENABLE_WEBRTC:-}" != "true" ]]; then
  ./x11vnc_startup.sh
fi

# -----------------------------------------------------------------------------
# House-keeping for the unprivileged "kernel" user --------------------------------
# Some Chromium subsystems want to create files under $HOME (NSS cert DB, dconf
# cache).  If those directories are missing or owned by root Chromium emits
# noisy error messages such as:
#   [ERROR:crypto/nss_util.cc:48] Failed to create /home/kernel/.pki/nssdb ...
#   dconf-CRITICAL **: unable to create directory '/home/kernel/.cache/dconf'
# Pre-create them and hand ownership to the user so the messages disappear.

dirs=(
  /home/kernel/.config/chromium
  /home/kernel/.pki/nssdb
  /home/kernel/.cache/dconf
)

for dir in "${dirs[@]}"; do
  # Skip if the path does not start with /home/kernel
  if [[ "$dir" != /home/kernel* ]]; then
    continue
  fi
  if [ ! -d "$dir" ]; then
    mkdir -p "$dir"
  fi
done

# Ensure correct ownership (ignore errors if already correct)
chown -R kernel:kernel /home/kernel/.config /home/kernel/.pki /home/kernel/.cache 2>/dev/null || true

# Start Chromium with display :1 and remote debugging, loading our recorder extension.
# Use ncat to listen on 0.0.0.0:9222 since chromium does not let you listen on 0.0.0.0 anymore: https://github.com/pyppeteer/pyppeteer/pull/379#issuecomment-217029626
cleanup () {
  echo "Cleaning up..."
  # Re-enable scale-to-zero if the script terminates early
  enable_scale_to_zero
  kill -TERM $pid
  kill -TERM $pid2
  if [ -n "${pulse_pid:-}" ]; then
    kill -TERM $pulse_pid 2>/dev/null || true
  fi
  # Kill the API server if it was started
  if [[ -n "${pid3:-}" ]]; then
    kill -TERM $pid3 || true
  fi
  if [ -n "${dbus_pid:-}" ]; then
    kill -TERM $dbus_pid 2>/dev/null || true
  fi
}
trap cleanup TERM INT
pid=
pid2=
pid3=
INTERNAL_PORT=9223
CHROME_PORT=9222  # External port mapped to host
echo "Starting Chromium on internal port $INTERNAL_PORT"

# Load additional Chromium flags from /chromium/flags if present
CHROMIUM_FLAGS="${CHROMIUM_FLAGS:-}"
if [[ -f /chromium/flags ]]; then
  CHROMIUM_FLAGS="$CHROMIUM_FLAGS $(cat /chromium/flags)"
fi
echo "CHROMIUM_FLAGS: $CHROMIUM_FLAGS"

RUN_AS_ROOT=${RUN_AS_ROOT:-false}
if [[ "$RUN_AS_ROOT" == "true" ]]; then
  DISPLAY=:1 DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" chromium \
    --remote-debugging-port=$INTERNAL_PORT \
    ${CHROMIUM_FLAGS:-} >&2 & pid=$!
else
  runuser -u kernel -- env \
    DISPLAY=:1 \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_SESSION_BUS_ADDRESS" \
    XDG_CONFIG_HOME=/home/kernel/.config \
    XDG_CACHE_HOME=/home/kernel/.cache \
    HOME=/home/kernel \
    chromium \
    --remote-debugging-port=$INTERNAL_PORT \
    ${CHROMIUM_FLAGS:-} >&2 & pid=$!
fi

echo "Setting up ncat proxy on port $CHROME_PORT"
ncat \
  --sh-exec "ncat 0.0.0.0 $INTERNAL_PORT" \
  -l "$CHROME_PORT" \
  --keep-open & pid2=$!

if [[ "${ENABLE_WEBRTC:-}" == "true" ]]; then
  # use webrtc
  echo "✨ Starting neko (webrtc server)."
  /usr/bin/neko serve --server.static /var/w