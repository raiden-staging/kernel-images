#!/usr/bin/env bash
set -ex -o pipefail

# Move to the script's directory so relative paths work regardless of the caller CWD
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR"

IMAGE="${IMAGE:-onkernel/kernel-cu-test:latest}"
NAME="${NAME:-kernel-cu-test}"

# Directory on host where recordings will be saved
HOST_RECORDINGS_DIR="$SCRIPT_DIR/recordings"
mkdir -p "$HOST_RECORDINGS_DIR"

# RUN_AS_ROOT defaults to false in docker
RUN_AS_ROOT="${RUN_AS_ROOT:-false}"

# Build Chromium flags file and mount
CHROMIUM_FLAGS_DEFAULT="--user-data-dir=/home/kernel/user-data --disable-dev-shm-usage --disable-gpu --start-maximized --disable-software-rasterizer --remote-allow-origins=*"
if [[ "$RUN_AS_ROOT" == "true" ]]; then
  CHROMIUM_FLAGS_DEFAULT="$CHROMIUM_FLAGS_DEFAULT --no-sandbox --no-zygote"
fi
CHROMIUM_FLAGS="${CHROMIUM_FLAGS:-$CHROMIUM_FLAGS_DEFAULT}"
rm -rf .tmp/chromium
mkdir -p .tmp/chromium
FLAGS_FILE="$(pwd)/.tmp/chromium/flags"
echo "$CHROMIUM_FLAGS" > "$FLAGS_FILE"

echo "flags file: $FLAGS_FILE"
cat "$FLAGS_FILE"

# Build docker run argument list
RUN_ARGS=(
  --name "$NAME"
  --privileged
  --tmpfs /dev/shm:size=2g
  -v "$HOST_RECORDINGS_DIR:/recordings"
  --memory 8192m
  -p 9222:9222
  -e DISPLAY_NUM=1
  -e HEIGHT=768
  -e WIDTH=1024
  -e RUN_AS_ROOT="$RUN_AS_ROOT"
  --mount type=bind,src="$FLAGS_FILE",dst=/chromium/flags,ro
)

# --- Host Audio Passthrough Configuration ---
# If ENABLE_HOST_AUDIO is true, mount the host's PulseAudio socket
# and cookie file into the container.
if [[ "${ENABLE_HOST_AUDIO:-}" == "true" ]]; then
  echo "Host audio enabled. Configuring PulseAudio passthrough."
  # Check that the host socket path is provided
  if [ -z "${HOST_PULSE_SOCKET:-}" ]; then
    echo "ERROR: ENABLE_HOST_AUDIO is true, but HOST_PULSE_SOCKET is not set." >&2
    exit 1
  fi
  RUN_ARGS+=(
    # Mount the host's PulseAudio socket
    -v "${HOST_PULSE_SOCKET}:/tmp/pulse-socket"
    # Tell applications inside the container to use this socket
    -e "PULSE_SERVER=unix:/tmp/pulse-socket"
    # Mount the host's PulseAudio cookie for authentication
    -v "${HOME}/.config/pulse/cookie:/home/kernel/.config/pulse/cookie:ro"
    # Match the container user to the host user for permissions
    --user "$(id -u):$(id -g)"
  )
fi

if [[ "${WITH_KERNEL_IMAGES_API:-}" == "true" ]]; then
  RUN_ARGS+=( -p 10001:10001 )
  RUN_ARGS+=( -e WITH_KERNEL_IMAGES_API=true )
fi

# noVNC vs WebRTC port mapping
if [[ "${ENABLE_WEBRTC:-}" == "true" ]]; then
  echo "Running container with WebRTC"
  RUN_ARGS+=( -p 8080:8080 )
  RUN_ARGS+=( -e ENABLE_WEBRTC=true )
  if [[ -n "${NEKO_ICESERVERS:-}" ]]; then
    RUN_ARGS+=( -e NEKO_ICESERVERS="$NEKO_ICESERVERS" )
  else
    RUN_ARGS+=( -e NEKO_WEBRTC_EPR=56000-56100 )
    RUN_ARGS+=( -e NEKO_WEBRTC_NAT1TO1=127.0.0.1 )
    RUN_ARGS+=( -p 56000-56100:56000-56100/udp )
  fi
else
  echo "Running container with noVNC"
  RUN_ARGS+=( -p 8080:6080 )
fi

docker rm -f "$NAME" 2>/dev/null || true
docker run -it "${RUN_ARGS[@]}" "$IMAGE"