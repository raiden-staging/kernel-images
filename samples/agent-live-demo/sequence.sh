#!/usr/bin/env bash
set -euo pipefail

# Usage: ./sequence.sh "<meeting_url>"

ROOT="$(cd "$(dirname "$0")" && pwd)"
MEETING_URL="${1:-}"
UV_PY_VERSION="${UV_PY_VERSION:-3.12}"
VENV_PATH="${VENV_PATH:-/tmp/agent-venv}"

RED='\033[1;31m'
GREEN='\033[1;32m'
YELLOW='\033[1;33m'
CYAN='\033[1;36m'
NC='\033[0m'

progress() {
  local pct="$1"; shift
  printf "${CYAN}[%3s%%]${NC} %s\n" "$pct" "$*"
}

die() { printf "${RED}ERROR:${NC} %s\n" "$*" >&2; exit 1; }

require_cmd() {
  local name="$1"
  command -v "$name" >/dev/null 2>&1 || die "Missing dependency: $name"
}

[ -z "$MEETING_URL" ] && die "meeting_url required. Usage: ./sequence.sh \"https://meet.google.com/...\""

# Load env (REMOTE_RTMP_URL, ELEVENLABS_API_KEY, etc.)
set -a
[ -f "$ROOT/.env" ] && . "$ROOT/.env"
set +a

require_cmd curl
require_cmd git
require_cmd docker
require_cmd ffmpeg
require_cmd node
require_cmd uv
if ! command -v screen >/dev/null 2>&1; then
  die "Missing dependency: screen (required to provide a TTY for kernel docker scripts)"
fi

[ -z "${REMOTE_RTMP_URL:-}" ] && die "REMOTE_RTMP_URL not set in environment/.env"
[ -z "${ELEVENLABS_API_KEY:-}" ] && die "ELEVENLABS_API_KEY not set in environment/.env"

KERNEL_DIR="${KERNEL_DIR:-/tmp/kernelmedia}"
KERNEL_LOG="${KERNEL_LOG:-/tmp/kernelmedia.log}"
CONDUCTOR_LOG="${CONDUCTOR_LOG:-/tmp/conductor.log}"
BROWSER_MODE="${BROWSER_MODE:-MOONDREAM_AGENT}"
AUTO_START_SESSION="${AUTO_START_SESSION:-1}"
SCREEN_SESSION="${SCREEN_SESSION:-kernelmedia}"

progress 0 "Stopping existing Docker containers..."
docker ps -q | xargs -r docker kill >/dev/null 2>&1 || true
progress 2 "Killing lingering conductor/ffmpeg processes..."
pkill -f "conductor.js" >/dev/null 2>&1 || true
pkill -f "uv .*webrtc_streamer.py" >/dev/null 2>&1 || true
pkill -f "ffmpeg" >/dev/null 2>&1 || true
progress 4 "Stopping any running kernel streams..."
curl -sf -X POST http://localhost:444/stream/stop >/dev/null 2>&1 || true

progress 5 "Starting kernel media stack (clone/build/run)..."
rm -rf "$KERNEL_DIR"
mkdir -p "$KERNEL_DIR"
rm -f "$KERNEL_LOG"
screen -S "$SCREEN_SESSION" -X quit >/dev/null 2>&1 || true
screen -S "$SCREEN_SESSION" -dm bash -c "
  {
    echo '[kernelmedia] started at ' \$(date)
    cd \"$KERNEL_DIR\" || exit 1
    git clone https://github.com/raiden-staging/kernel-images.git -b media@v1@rev2
    cd kernel-images/images/chromium-headful || exit 1
    IMAGE=kernel-docker ./build-docker.sh
    CHROMIUM_FLAGS=\"--user-data-dir=/home/kernel/user-data --disable-dev-shm-usage --disable-gpu --start-maximized --disable-software-rasterizer --remote-allow-origins=* --auto-accept-camera-and-microphone-capture --auto-select-desktop-capture-source='Virtual Input Feed' --allow-http-screen-capture\" IMAGE=kernel-docker ENABLE_WEBRTC=true ./run-docker.sh
  } 2>&1 | tee -a \"$KERNEL_LOG\"
"

progress 12 "Waiting for kernel API on :444 (logs -> $KERNEL_LOG)..."
MAX_WAIT=300
ELAPSED=0
until curl -sf "http://localhost:444/input/devices/virtual/status" >/dev/null 2>&1; do
  sleep 2
  ELAPSED=$((ELAPSED + 2))
  if ! screen -list | grep -q "$SCREEN_SESSION"; then
    die "Kernel process exited; see $KERNEL_LOG"
  fi
  [ "$ELAPSED" -ge "$MAX_WAIT" ] && die "Kernel API not ready after ${MAX_WAIT}s; see $KERNEL_LOG"
done
progress 25 "Kernel API ready."

progress 28 "Stopping any existing kernel streams..."
curl -sf -X POST http://localhost:444/stream/stop >/dev/null 2>&1 || true

progress 30 "Installing JS deps (bun/npm) if needed..."
if command -v bun >/dev/null 2>&1; then
  (cd "$ROOT" && bun install) >/tmp/agent-bun-install.log 2>&1 || die "bun install failed (see /tmp/agent-bun-install.log)"
else
  (cd "$ROOT" && npm install) >/tmp/agent-npm-install.log 2>&1 || die "npm install failed (see /tmp/agent-npm-install.log)"
fi
progress 40 "Deps installed."

progress 42 "Ensuring Python $UV_PY_VERSION via uv..."
uv python install "$UV_PY_VERSION" >/tmp/agent-uv-install.log 2>&1 || die "uv python install failed (see /tmp/agent-uv-install.log)"
progress 43 "Preparing Python venv via uv..."
rm -rf "$VENV_PATH"
uv venv --python "$UV_PY_VERSION" "$VENV_PATH" >>/tmp/agent-uv-install.log 2>&1 || die "uv venv failed (see /tmp/agent-uv-install.log)"
UV_PY="$VENV_PATH/bin/python"
progress 44 "Installing Python deps (browser-use, playwright)..."
uv pip install --python "$UV_PY" browser-use python-dotenv openai playwright >>/tmp/agent-uv-install.log 2>&1 || die "uv pip install failed (see /tmp/agent-uv-install.log)"

cleanup() {
  printf "${YELLOW}Cleaning up processes and streams...${NC}\n"
  docker ps -q | xargs -r docker kill >/dev/null 2>&1 || true
  pkill -f "conductor.js" >/dev/null 2>&1 || true
  pkill -f "uv .*webrtc_streamer.py" >/dev/null 2>&1 || true
  pkill -f "ffmpeg" >/dev/null 2>&1 || true
  curl -sf -X POST http://localhost:444/stream/stop >/dev/null 2>&1 || true
}
trap cleanup INT TERM

progress 45 "Starting conductor server (logs -> $CONDUCTOR_LOG)..."
cd "$ROOT"
UV_PYTHON="$UV_PY" node conductor.js --meeting_url "$MEETING_URL" >"$CONDUCTOR_LOG" 2>&1 &
CONDUCTOR_PID=$!

ELAPSED=0
until grep -q "Conductor server listening" "$CONDUCTOR_LOG" 2>/dev/null; do
  sleep 1
  ELAPSED=$((ELAPSED + 1))
  if ! kill -0 "$CONDUCTOR_PID" 2>/dev/null; then
    die "Conductor exited; see $CONDUCTOR_LOG"
  fi
  [ "$ELAPSED" -ge 60 ] && die "Conductor not ready after 60s; see $CONDUCTOR_LOG"
done
progress 55 "Conductor server up."

progress 65 "Triggering prepare (virtual inputs + livestreams)..."
if curl -sf -X POST http://localhost:3117/prepare >/tmp/conductor-prepare.log 2>&1; then
  progress 75 "Prepare complete."
  sleep 2
else
  progress 70 "Prepare failed, cleaning up (docker + processes)..."
  docker ps -q | xargs -r docker kill >/dev/null 2>&1 || true
  pkill -f "conductor.js" >/dev/null 2>&1 || true
  pkill -f "uv .*webrtc_streamer.py" >/dev/null 2>&1 || true
  pkill -f "ffmpeg" >/dev/null 2>&1 || true
  curl -sf -X POST http://localhost:444/stream/stop >/dev/null 2>&1 || true
  die "Prepare failed (see /tmp/conductor-prepare.log)"
fi

if [ "$BROWSER_MODE" = "AUTO_AGENT" ]; then
  progress 90 "Launching browser automation (python browser-use)..."
  if UV_PYTHON="$UV_PY" uv run --python "$UV_PY" scripts/browser_join.py --meeting_url "$MEETING_URL" >/tmp/conductor-join.log 2>&1; then
    progress 94 "Browser automation triggered."
  else
    progress 92 "Browser join failed, cleaning up (docker + processes + streams)..."
    docker ps -q | xargs -r docker kill >/dev/null 2>&1 || true
    pkill -f "conductor.js" >/dev/null 2>&1 || true
    pkill -f "uv .*webrtc_streamer.py" >/dev/null 2>&1 || true
    pkill -f "ffmpeg" >/dev/null 2>&1 || true
    curl -sf -X POST http://localhost:444/stream/stop >/dev/null 2>&1 || true
    die "Browser join failed (see /tmp/conductor-join.log)"
  fi
elif [ "$BROWSER_MODE" = "MOONDREAM_AGENT" ]; then
  progress 90 "MOONDREAM_AGENT: Opening tabs via CDP..."
  if UV_PYTHON="$UV_PY" uv run --python "$UV_PY" scripts/open_tabs.py --meeting_url "$MEETING_URL" >/tmp/conductor-open-tabs.log 2>&1; then
    # progress 92 "Tabs opened. Sleeping 2s before MOONDREAM..."
    # sleep 2
    progress 93 "Running Moondream Browser Agent..."
    if node scripts/moondream_and_kernelapi_agent.js >/tmp/moondream-agent.log 2>&1; then
       progress 95 "Browser Agent finished successfully."
    else
       die "Browser Agent failed (see /tmp/moondream-agent.log)"
    fi
  else
    die "Failed to open tabs (see /tmp/conductor-open-tabs.log)"
  fi
else
  progress 90 "MANUAL_DEBUG: Opening tabs via CDP..."
  if UV_PYTHON="$UV_PY" uv run --python "$UV_PY" scripts/open_tabs.py --meeting_url "$MEETING_URL" >/tmp/conductor-open-tabs.log 2>&1; then
    progress 92 "Tabs opened. Waiting 35s for manual interaction..."
    sleep 35
  else
    die "Failed to open tabs (see /tmp/conductor-open-tabs.log)"
  fi
fi

progress 97 "Starting ElevenLabs session + media pipelines..."
curl -sf -X POST http://localhost:3117/session/start >/tmp/conductor-session.log 2>&1 || die "Session start failed (see /tmp/conductor-session.log)"
progress 100 "${GREEN}All systems ready.${NC}"

printf "${YELLOW}Next steps:${NC}\n"
printf "  - To start/stop ElevenLabs: curl -X POST http://localhost:3117/session/start|stop\n"
printf "  - Logs: kernel -> %s , conductor -> %s\n" "$KERNEL_LOG" "$CONDUCTOR_LOG"
