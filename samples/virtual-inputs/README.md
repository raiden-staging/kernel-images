# Virtual input samples

These examples target `/input/devices/virtual/*` and pair with the fullscreen preview page served from `/input/devices/virtual/feed`. Override the preview with `fit` (CSS object-fit) or `source` query params as needed.

Local media helpers live in `samples/virtual-inputs/media/`:
- `sample_video.ts` (small MPEG-TS video)
- `sample_video.mp4` (longer MP4 clip)
- `sample_audio.mp3` and `sample_audio.wav`

## HTTP/HLS inputs
Configure both video and audio from URLs and immediately preview them in the feed page:
```bash
curl -s http://localhost:10001/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "stream", "url": "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"},
    "audio": {"type": "stream", "url": "http://icecast.err.ee/r2rock.opus"},
    "width": 1280, "height": 720, "frame_rate": 30
  }' | jq
```
Open the preview: `http://localhost:10001/input/devices/virtual/feed` (auto-detects the configured video source).

## WebSocket MPEG-TS ingest
Send MPEG-TS chunks over a websocket into the virtual camera. The server expects binary MPEG-TS by default and mirrors the feed to `/input/devices/virtual/feed`.
1. Configure the pipeline to wait for socket input:
   ```bash
   curl -s http://localhost:10001/input/devices/virtual/configure \
     -H "Content-Type: application/json" \
     -d '{"video":{"type":"socket","format":"mpegts"},"audio":{"type":"file","url":"samples/virtual-inputs/media/sample_audio.mp3","loop":true}}' | jq
   ```
2. Push a local TS clip with Node.js (ESM):
   ```bash
   node - <<'NODE'
   import { readFile } from 'node:fs/promises';
   import WebSocket from 'ws';
   const media = await readFile('samples/virtual-inputs/media/sample_video.ts');
   const ws = new WebSocket('ws://localhost:10001/input/devices/virtual/socket/video');
   ws.binaryType = 'arraybuffer';
   ws.on('open', () => ws.send(media));
   ws.on('message', msg => console.log('server format hint:', msg.toString()));
   ws.on('close', () => console.log('ingest closed'));
   NODE
   ```
3. Python variant (uses `websockets`):
   ```bash
   python - <<'PY'
   import asyncio, websockets
   async def main():
       data = open('samples/virtual-inputs/media/sample_video.ts','rb').read()
       async with websockets.connect('ws://localhost:10001/input/devices/virtual/socket/video') as ws:
           try:
               fmt = await asyncio.wait_for(ws.recv(), timeout=1)
               if isinstance(fmt, str):
                   print('format hint:', fmt)
           except Exception:
               pass
           await ws.send(data)
   asyncio.run(main())
   PY
   ```
4. View the feed while ingesting: `http://localhost:10001/input/devices/virtual/feed?fit=cover`

## WebRTC ingest
Use WebRTC when the publisher prefers SDP negotiation. The `/input/devices/virtual/feed` page mirrors the incoming IVF stream via WebCodecs.
```bash
curl -s http://localhost:10001/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"video":{"type":"webrtc"},"audio":{"type":"file","url":"samples/virtual-inputs/media/sample_audio.wav"}}' | jq
```
Minimal `aiortc` publisher using the bundled MP4 sample:
```bash
python - <<'PY'
import asyncio, aiohttp
from aiortc import RTCPeerConnection, RTCSessionDescription, MediaPlayer

async def main():
    pc = RTCPeerConnection()
    player = MediaPlayer('samples/virtual-inputs/media/sample_video.mp4')
    if player.video:
        pc.addTrack(player.video)
    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as session:
        resp = await session.post('http://localhost:10001/input/devices/virtual/webrtc/offer', json={'sdp': pc.localDescription.sdp})
        answer = await resp.json()
    await pc.setRemoteDescription(RTCSessionDescription(sdp=answer['sdp'], type='answer'))
    print('Streaming... press Ctrl+C to stop')
    await asyncio.Future()

asyncio.run(main())
PY
```
When the WebRTC negotiation completes, reload `/input/devices/virtual/feed` to watch the mirrored stream.

## Pause/stop helpers
- Pause the pipeline (sends black frames/silence): `curl -X POST http://localhost:10001/input/devices/virtual/pause`
- Resume live media: `curl -X POST http://localhost:10001/input/devices/virtual/resume`
- Stop and reset everything (also clears the preview websocket): `curl -X POST http://localhost:10001/input/devices/virtual/stop`
