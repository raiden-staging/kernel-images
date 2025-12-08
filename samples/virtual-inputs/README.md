# Virtual input samples

These examples target `/input/devices/virtual/*` and pair with the fullscreen preview page served from `/input/devices/virtual/feed`. When using the Docker helpers, host traffic should go to `http://localhost:444/...`; inside the container the API listens on `10001`. Override the preview with `fit` (CSS object-fit) or `source` query params as needed.

Local media helpers live in `samples/virtual-inputs/media/`:
- `sample_video.ts` (small MPEG-TS video)
- `sample_video.mp4` (longer MP4 clip)
- `sample_audio.mp3` and `sample_audio.wav`

## HTTP/HLS inputs
Configure both video and audio from URLs and immediately preview them in the feed page:
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "stream", "url": "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"},
    "audio": {"type": "stream", "url": "http://icecast.err.ee/r2rock.opus"},
    "width": 1280, "height": 720, "frame_rate": 30
  }' | jq
```
Open the preview: `http://localhost:444/input/devices/virtual/feed` (use port `10001` only from inside the container).

## WebSocket ingest (chunked)
Keep both media directions on websockets—no mixing with file inputs. Default to MPEG-TS for video and MP3 for audio:
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "socket", "format": "mpegts"},
    "audio": {"type": "socket", "format": "mp3"},
    "width": 1280, "height": 720, "frame_rate": 30
  }' | jq
```
Chunk TS + MP3 samples over the sockets with Node.js (keeps the sockets open so the pipeline stays alive):
```bash
node - <<'NODE'
import { createReadStream } from 'node:fs';
import { once } from 'node:events';
import WebSocket from 'ws';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function pump(url, path, label, throttleMs = 35) {
  const ws = new WebSocket(url);
  ws.binaryType = 'arraybuffer';
  ws.on('message', (msg) => console.log(`${label} format hint:`, msg.toString()));
  await once(ws, 'open');
  console.log(`${label} connected`);

  for await (const chunk of createReadStream(path, { highWaterMark: 64 * 1024 })) {
    ws.send(chunk);
    await delay(throttleMs);
  }

  console.log(`${label} file sent; keep the socket open to push more chunks or switch to a live source`);
  return ws;
}

const video = await pump(
  'ws://localhost:444/input/devices/virtual/socket/video',
  'samples/virtual-inputs/media/sample_video.ts',
  'video'
);
const audio = await pump(
  'ws://localhost:444/input/devices/virtual/socket/audio',
  'samples/virtual-inputs/media/sample_audio.mp3',
  'audio'
);

console.log('Streaming... press Ctrl+C to stop or send extra chunks manually');
await new Promise(() => {});
NODE
```
To stream MP4 over the websocket instead, reconfigure with `"format": "mp4"` for the video source and point the `video` path in the snippet to `samples/virtual-inputs/media/sample_video.mp4`. MP4 requires the full header before playback, so let the chunker finish before expecting video in the preview.

Open the live preview while the sockets run: `http://localhost:444/input/devices/virtual/feed?fit=cover`

## WebRTC ingest (Python)
Prepare the ingest endpoints for WebRTC (both tracks stay on the same transport):
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"video":{"type":"webrtc"},"audio":{"type":"webrtc"}}' | jq
```
Use a self-contained shell helper that installs dependencies via `uv` and sends a local MP4 track:
```bash
cat > run_webrtc.sh <<'EOF'
#!/usr/bin/env sh
set -e

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

"$UV" venv .venv
. .venv/bin/activate
"$UV" pip install aiohttp aiortc

$PY - <<'PY'
import asyncio, aiohttp
from pathlib import Path
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer

async def main():
    pc = RTCPeerConnection()
    media = Path('samples/virtual-inputs/media/sample_video.mp4').resolve()
    player = MediaPlayer(media.as_posix())
    if player.video:
        pc.addTrack(player.video)
    if player.audio:
        pc.addTrack(player.audio)
    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as session:
        resp = await session.post(
            'http://localhost:444/input/devices/virtual/webrtc/offer',
            json={'sdp': pc.localDescription.sdp}
        )
        answer = await resp.json()

    await pc.setRemoteDescription(
        RTCSessionDescription(sdp=answer['sdp'], type='answer')
    )
    print('Streaming... press Ctrl+C to stop')
    await asyncio.Future()

asyncio.run(main())
PY
EOF

chmod +x run_webrtc.sh
./run_webrtc.sh
```
When the WebRTC negotiation completes, reload `/input/devices/virtual/feed` to watch the mirrored stream.

## Pause/stop helpers
- Pause the pipeline (sends black frames/silence): `curl -X POST http://localhost:444/input/devices/virtual/pause`
- Resume live media: `curl -X POST http://localhost:444/input/devices/virtual/resume`
- Stop and reset everything (also clears the preview websocket): `curl -X POST http://localhost:444/input/devices/virtual/stop`
