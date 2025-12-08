#!/usr/bin/env sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"
VENV_PATH="${VENV_PATH:-/tmp/kernel-virtual-inputs-webrtc-venv}"

if command -v python3 >/dev/null 2>&1; then
    PY=python3
elif command -v python >/dev/null 2>&1; then
    PY=python
else
    echo "no python interpreter found"
    exit 1
fi

UV=""
[ -x "$HOME/.local/bin/uv" ] && UV="$HOME/.local/bin/uv"
[ -x "/usr/local/bin/uv" ] && UV="/usr/local/bin/uv"
[ -x "/usr/bin/uv" ] && UV="/usr/bin/uv"

if [ -z "$UV" ]; then
    curl -LsSf https://astral.sh/uv/install.sh | sh
    [ -x "$HOME/.local/bin/uv" ] && UV="$HOME/.local/bin/uv"
    [ -x "/usr/local/bin/uv" ] && UV="/usr/local/bin/uv"
    [ -x "/usr/bin/uv" ] && UV="/usr/bin/uv"
fi

if [ -z "$UV" ]; then
    echo "uv installation failed"
    exit 1
fi

"$UV" venv "$VENV_PATH"
. "$VENV_PATH/bin/activate"
"$UV" pip install aiohttp aiortc

API_URL="${API_URL:-http://localhost:444/input/devices/virtual/webrtc/offer}"

$PY - <<PY
import asyncio, aiohttp
from pathlib import Path
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer

async def main():
    pc = RTCPeerConnection()
    media = Path("media/sample_video.mp4").resolve()
    player = MediaPlayer(media.as_posix())
    if player.video:
        pc.addTrack(player.video)
    if player.audio:
        pc.addTrack(player.audio)
    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as session:
        resp = await session.post(
            "${API_URL}",
            json={"sdp": pc.localDescription.sdp},
        )
        answer = await resp.json()

    await pc.setRemoteDescription(
        RTCSessionDescription(sdp=answer["sdp"], type="answer")
    )
    print("Streaming... press Ctrl+C to stop")
    await asyncio.Future()

asyncio.run(main())
PY
