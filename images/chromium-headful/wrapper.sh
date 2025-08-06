#!/bin/bash

set -o pipefail -o errexit -o nounset

# If PULSE_SERVER is not set from the host, we need to start our own internal services.
if [ -z "${PULSE_SERVER:-}" ]; then
  echo "No host PULSE_SERVER found. Starting internal audio services..."
  export PULSE_SERVER=unix:/tmp/pulseaudio.socket

  # ---------------------------------------------------------------------------
  # System-bus setup (Internal Mode)
  # ---------------------------------------------------------------------------
  echo "Starting system D-Bus daemon"
  mkdir -p /run/dbus
  dbus-uuidgen --ensure
  dbus-daemon --system --address=unix:path=/run/dbus/system_bus_socket --nopidfile --nosyslog --nofork &
  dbus_pid=$!

  echo "Waiting for D-Bus socket..."
  for i in $(seq 1 20); do [ -S /run/dbus/system_bus_socket ] && break || sleep 0.5; done
  if [ ! -S /run/dbus/system_bus_socket ]; then echo "D-Bus socket not found!" >&2; exit 1; fi
  echo "D-Bus socket is available."

  export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/dbus/system_bus_socket"

  # Start PulseAudio as the 'kernel' user
  echo "Starting PulseAudio daemon..."
  runuser -u kernel -- env XDG_RUNTIME_DIR=/tmp/runtime-kernel \
    pulseaudio --log-level=error --disallow-module-loading --disallow-exit --exit-idle-time=-1 &
  pulse_pid=$!

  echo "Waiting for PulseAudio socket..."
  for i in $(seq 1 20); do [ -S "$PULSE_SERVER" ] && break || sleep 0.5; done
  if ! kill -0 $pulse_pid 2>/dev/null || [ ! -S "$PULSE_SERVER" ]; then echo "PulseAudio failed to start!" >&2; exit 1; fi
  echo "PulseAudio socket is available."
else
  echo "Host PULSE_SERVER detected at '${PULSE_SERVER}'. Skipping internal audio setup."
fi


# If the WITHDOCKER environment variable is not set, it means we are not running inside a Docker container.
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

if [[ "${ENABLE_WEBRTC:-}" != "true" ]]; then
  ./x11vnc_startup.sh
fi

# -----------------------------------------------------------------------------
# House-keeping for the unprivileged "kernel" user --------------------------------
dirs=(
  /home/kernel/.config/chromium
  /home/kernel/.pki/nssdb
  /home/kernel/.cache/dconf
  /tmp/runtime-kernel/dconf
)

for dir in "${dirs[@]}"; do
  mkdir -p "$dir"
done
chown -R kernel:kernel /home/kernel/.config /home/kernel/.pki /home/kernel/.cache /tmp/runtime-kernel 2>/dev/null || true

# Start Chromium with display :1 and remote debugging
cleanup () {
  echo "Cleaning up..."
  enable_scale_to_zero
  kill -TERM $pid
  kill -TERM $pid2
  # Only try to kill daemons if they were started
  if [ -n "${pulse_pid:-}" ]; then kill -TERM $pulse_pid 2>/dev/null || true; fi
  if [ -n "${dbus_pid:-}" ]; then kill -TERM $dbus_pid 2>/dev/null || true; fi
  if [[ -n "${pid3:-}" ]]; then kill -TERM $pid3 || true; fi
}
trap cleanup TERM INT
pid=
pid2=
pid3=
INTERNAL_PORT=9223
CHROME_PORT=9222
echo "Starting Chromium on internal port $INTERNAL_PORT"

CHROMIUM_FLAGS="${CHROMIUM_FLAGS:-}"
if [[ -f /chromium/flags ]]; then
  CHROMIUM_FLAGS="$CHROMIUM_FLAGS $(cat /chromium/flags)"
fi
echo "CHROMIUM_FLAGS: $CHROMIUM_FLAGS"

RUN_AS_ROOT=${RUN_AS_ROOT:-false}
if [[ "$RUN_AS_ROOT" == "true" ]]; then
  DISPLAY=:1 chromium --remote-debugging-port=$INTERNAL_PORT ${CHROMIUM_FLAGS:-} >&2 & pid=$!
else
  runuser -u kernel -- env XDG_CONFIG_HOME=/home/kernel/.config XDG_CACHE_HOME=/home/kernel/.cache HOME=/home/kernel \
    chromium --remote-debugging-port=$INTERNAL_PORT ${CHROMIUM_FLAGS:-} >&2 & pid=$!
fi

echo "Setting up ncat proxy on port $CHROME_PORT"
ncat --sh-exec "ncat 0.0.0.0 $INTERNAL_PORT" -l "$CHROME_PORT" --keep-open & pid2=$!

if [[ "${ENABLE_WEBRTC:-}" == "true" ]]; then
  echo "✨ Starting neko (webrtc server)."
  /usr/bin/neko serve --server.static /var/www --server.bind 0.0.0.0:8080 >&2 &
  echo "Waiting for neko port 0.0.0.0:8080..."
  while ! nc -z 127.0.0.1 8080 2>/dev/null; do sleep 0.5; done
  echo "Port 8080 is open"
else
  ./novnc_startup.sh
  echo "✨ noVNC demo is ready to use!"
fi

if [[ "${WITH_KERNEL_IMAGES_API:-}" == "true" ]]; then
  echo "✨ Starting kernel-images API."
  API_PORT="${KERNEL_IMAGES_API_PORT:-10001}"
  API_FRAME_RATE="${KERNEL_IMAGES_API_FRAME_RATE:-10}"
  API_DISPLAY_NUM="${KERNEL_IMAGES_API_DISPLAY_NUM:-${DISPLAY_NUM:-1}}"
  API_MAX_SIZE_MB="${KERNEL_IMAGES_API_MAX_SIZE_MB:-500}"
  API_OUTPUT_DIR="${KERNEL_IMAGES_API_OUTPUT_DIR:-/recordings}"
  mkdir -p "$API_OUTPUT_DIR"
  PORT="$API_PORT" FRAME_RATE="$API_FRAME_RATE" DISPLAY_NUM="$API_DISPLAY_NUM" MAX_SIZE_MB="$API_MAX_SIZE_MB" OUTPUT_DIR="$API_OUTPUT_DIR" \
  /usr/local/bin/kernel-images-api & pid3=$!
fi

if [[ -z "${WITHDOCKER:-}" ]]; then
  enable_scale_to_zero
fi

# Keep the container running
tail -f /dev/null