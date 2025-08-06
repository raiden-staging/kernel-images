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
# D-Bus and PulseAudio User Session Setup -------------------------------------
# -----------------------------------------------------------------------------
# Launch a session-specific D-Bus instance for the 'kernel' user.
# This is the correct way to provide a bus for user-level applications
# like PulseAudio and Chromium, avoiding system bus permission issues.
echo "Launching D-Bus session for user 'kernel'..."
# The 'eval' command sets DBUS_SESSION_BUS_ADDRESS and DBUS_SESSION_BUS_PID
eval $(runuser -u kernel -- dbus-launch --sh-syntax)
if [ -z "${DBUS_SESSION_BUS_PID:-}" ]; then
    echo "Failed to launch D-Bus session." >&2
    exit 1
fi
echo "D-Bus session started with PID: $DBUS_SESSION_BUS_PID"

# Export the address for all subsequent child processes run by this script.
export DBUS_SESSION_BUS_ADDRESS

# Now start PulseAudio as the 'kernel' user. It will connect to its own D-Bus session.
echo "Starting PulseAudio daemon..."
runuser -u kernel -- pulseaudio --log-level=error --disallow-module-loading --disallow-exit --exit-idle-time=-1 &
pulse_pid=$!

# Wait for pulseaudio socket to be available, with a timeout
echo "Waiting for PulseAudio socket..."
for i in $(seq 1 20); do
  if [ -S "$PULSE_SERVER" ]; then
    break
  fi
  # check if pulseaudio process is still alive
  if ! kill -0 $pulse_pid 2>/dev/null; then
    echo "PulseAudio process died. Aborting." >&2
    exit 1
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
  if [ -n "${DBUS_SESSION_BUS_PID:-}" ]; then
    kill -TERM $DBUS_SESSION_BUS_PID 2>/dev/null || true
  fi
  # Kill the API server if it was started
  if [[ -n "${pid3:-}" ]]; then
    kill -TERM $pid3 || true
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
  DISPLAY=:1 chromium \
    --remote-debugging-port=$INTERNAL_PORT \
    ${CHROMIUM_FLAGS:-} >&2 & pid=$!
else
  # The required environment variables (DISPLAY, DBUS_SESSION_BUS_ADDRESS) are already exported
  # in this script's environment, so runuser will pass them to the child process.
  runuser -u kernel -- env \
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
  /usr/bin/neko serve --server.static /var/www --server.bind 0.0.0.0:8080 >&2 &

  # Wait for neko to be ready.
  echo "Waiting for neko port 0.0.0.0:8080..."
  while ! nc -z 127.0.0.1 8080 2>/dev/null; do
    sleep 0.5
  done
  echo "Port 8080 is open"
else
  # use novnc
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

  PORT="$API_PORT" \
  FRAME_RATE="$API_FRAME_RATE" \
  DISPLAY_NUM="$API_DISPLAY_NUM" \
  MAX_SIZE_MB="$API_MAX_SIZE_MB" \
  OUTPUT_DIR="$API_OUTPUT_DIR" \
  /usr/local/bin/kernel-images-api & pid3=$!
  # close the "--no-sandbox unsupported flag" warning when running as root
  # in the unikernel runtime we haven't been able to get chromium to launch as non-root without cryptic crashpad errors
  # and when running as root you must use the --no-sandbox flag, which generates a warning
  if [[ "${RUN_AS_ROOT:-}" == "true" ]]; then
    echo "Running as root, attempting to dismiss the --no-sandbox unsupported flag warning"
    if read -r WIDTH HEIGHT <<< "$(xdotool getdisplaygeometry 2>/dev/null)"; then
      # Work out an x-coordinate slightly inside the right-hand edge of the
      OFFSET_X=$(( WIDTH - 30 ))
      if (( OFFSET_X < 0 )); then
        OFFSET_X=0
      fi

      # Wait for kernel-images API port 10001 to be ready.
      echo "Waiting for kernel-images API port 127.0.0.1:10001..."
      while ! nc -z 127.0.0.1 10001 2>/dev/null; do
        sleep 0.5
      done
      echo "Port 10001 is open"

      # Wait for Chromium window to open before dismissing the --no-sandbox warning.
      target='New Tab - Chromium'
      echo "Waiting for Chromium window \"${target}\" to appear and become active..."
      while :; do
        win_id=$(xwininfo -root -tree 2>/dev/null | awk -v t="$target" '$0 ~ t {print $1; exit}')
        if [[ -n $win_id ]]; then
          win_id=${win_id%:}
          if xdotool windowactivate --sync "$win_id"; then
            echo "Focused window $win_id ($target) on $DISPLAY"
            break
          fi
        fi
        sleep 0.5
      done

      # wait... not sure but this just increases the likelihood of success
      # without the sleep you often open the live view and see the mouse hovering over the "X" to dismiss the warning, suggesting that it clicked before the warning or chromium appeared
      sleep 5

      # Attempt to click the warning's close button
      echo "Clicking the warning's close button at x=$OFFSET_X y=115"
      if curl -s -o /dev/null -X POST \
        http://localhost:10001/computer/click_mouse \
        -H "Content-Type: application/json" \
        -d "{\"x\":${OFFSET_X},\"y\":115}"; then
          echo "Successfully clicked the warning's close button"
      else
        echo "Failed to click the warning's close button" >&2
      fi
    else
      echo "xdotool failed to obtain display geometry; skipping sandbox warning dismissal." >&2
    fi
  fi
fi

if [[ -z "${WITHDOCKER:-}" ]]; then
  enable_scale_to_zero
fi

# Keep the container running
tail -f /dev/null