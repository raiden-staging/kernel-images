# Virtual Inputs & Livestreaming

Real-time media injection and broadcast capabilities for the Kernel browser sandbox.

## Overview

| Feature | Protocols | Audio Support |
|---------|-----------|---------------|
| Virtual Inputs | WebSocket, WebRTC, HTTP/HLS, File | Yes |
| Livestreaming | WebSocket, WebRTC, RTMP/RTMPS | Yes |

**Ports**: External access via `444`, internal container access via `10001`.

---

# Part 1: Utilities

## Virtual Inputs

Feed video/audio into the container's virtual webcam and microphone.

### Video: WebRTC Feed

Real-time video injection via WebRTC. ~0 latency peer connection.

**Configure:**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"video":{"type":"webrtc"},"audio":{"type":"webrtc"}}' | jq
```

**Response:**
```json
{
  "state": "running",
  "mode": "virtual-file",
  "video_device": "/dev/video0",
  "audio_sink": "audio_input",
  "microphone_source": "audio_input.monitor",
  "ingest": {
    "video": {
      "protocol": "webrtc",
      "format": "ivf",
      "url": "http://localhost:10001/input/devices/virtual/webrtc/offer"
    },
    "audio": {
      "protocol": "webrtc",
      "format": "opus",
      "destination": "microphone",
      "url": "http://localhost:10001/input/devices/virtual/webrtc/offer"
    }
  }
}
```

**Stream with Python (aiortc):**
```bash
cd samples/virtual-inputs
sh run_webrtc.sh
```

Or inline:
```python
import asyncio, aiohttp
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer

async def main():
    pc = RTCPeerConnection()
    player = MediaPlayer("media/sample_video.mp4")
    if player.video:
        pc.addTrack(player.video)
    if player.audio:
        pc.addTrack(player.audio)
    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as session:
        resp = await session.post(
            "http://localhost:444/input/devices/virtual/webrtc/offer",
            json={"sdp": pc.localDescription.sdp},
        )
        answer = await resp.json()

    await pc.setRemoteDescription(
        RTCSessionDescription(sdp=answer["sdp"], type="answer")
    )
    print("Streaming... Ctrl+C to stop")
    await asyncio.Future()

asyncio.run(main())
```

**Real-time factor**: WebRTC maintains ~50-150ms latency. Feed page reflects current frame only - no buffering/replay.

---

### Video: WebSocket Feed

Chunk-based MPEG-TS streaming over WebSocket. Real-time ingest with ~100-200ms latency.

**Configure:**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "socket", "format": "mpegts", "width": 1280, "height": 720, "frame_rate": 30},
    "audio": {"type": "socket", "format": "mp3"}
  }' | jq
```

**Response:**
```json
{
  "state": "running",
  "mode": "virtual-file",
  "ingest": {
    "video": {
      "protocol": "socket",
      "format": "mpegts",
      "url": "ws://localhost:10001/input/devices/virtual/socket/video"
    },
    "audio": {
      "protocol": "socket",
      "format": "mp3",
      "destination": "microphone",
      "url": "ws://localhost:10001/input/devices/virtual/socket/audio"
    }
  }
}
```

**Stream with Node.js:**
```bash
npm install ws
node samples/virtual-inputs/ws_chunk_ingest.js
```

Or inline:
```javascript
import { createReadStream } from 'node:fs';
import WebSocket from 'ws';

const ws = new WebSocket('ws://localhost:444/input/devices/virtual/socket/video');
ws.on('open', async () => {
  for await (const chunk of createReadStream('sample_video_mpeg1.ts', { highWaterMark: 65536 })) {
    ws.send(chunk);
    await new Promise(r => setTimeout(r, 35)); // ~real-time pacing
  }
});
```

**MPEG-1 encoding requirement** (for JSMpeg playback):
```bash
ffmpeg -i input.mp4 -c:v mpeg1video -b:v 1500k -f mpegts output.ts
```

**Real-time factor**: No caching. Page shows "Loading..." when no chunks arriving. Resume shows current frame only.

---

### Audio: WebRTC Feed

Real-time audio injection into virtual microphone.

**Configure (to virtual mic):**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"audio":{"type":"webrtc"}}' | jq
```

**Configure (to speaker output):**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"audio":{"type":"webrtc","destination":"speaker"}}' | jq
```

**Audio destinations:**
| Destination | PulseAudio Sink | Use Case |
|-------------|-----------------|----------|
| `microphone` (default) | `audio_input` | Apps reading mic input receive this audio |
| `speaker` | `audio_output` | Direct playback/monitoring |

---

### Audio: WebSocket Feed

MP3 chunk streaming to virtual microphone.

**Configure (to virtual mic):**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"audio":{"type":"socket","format":"mp3"}}' | jq
```

**Configure (to speaker):**
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"audio":{"type":"socket","format":"mp3","destination":"speaker"}}' | jq
```

**Stream MP3 chunks:**
```javascript
import { createReadStream } from 'node:fs';
import WebSocket from 'ws';

const ws = new WebSocket('ws://localhost:444/input/devices/virtual/socket/audio');
ws.on('open', async () => {
  for await (const chunk of createReadStream('sample_audio.mp3', { highWaterMark: 65536 })) {
    ws.send(chunk);
    await new Promise(r => setTimeout(r, 35));
  }
});
```

---

### Feed Page

View the virtual input stream in a fullscreen browser page.

**URL:** `http://localhost:444/input/devices/virtual/feed`

**Query params:**
- `fit`: CSS object-fit value (`cover`, `contain`, `fill`)
- `source`: Override video source

**Behavior:**
- Shows "Loading virtual feed..." when no chunks arriving
- Displays current frame only - no caching/replay
- Refresh shows current state, not buffered history

**Discover WebSocket feed info:**
```bash
curl -s http://localhost:444/input/devices/virtual/feed/socket/info | jq
```

**Response:**
```json
{
  "url": "ws://localhost:10001/input/devices/virtual/feed/socket",
  "format": "mpegts"
}
```

**Capture feed to file:**
```bash
node samples/virtual-inputs/feed_capture.js
# Output: feed_capture.mpegts (or .ivf for WebRTC)
```

---

### Control Endpoints

**Pause (black frames/silence):**
```bash
curl -X POST http://localhost:444/input/devices/virtual/pause
```

**Resume:**
```bash
curl -X POST http://localhost:444/input/devices/virtual/resume
```

**Stop:**
```bash
curl -X POST http://localhost:444/input/devices/virtual/stop
```

**Status:**
```bash
curl -s http://localhost:444/input/devices/virtual/status | jq
```

---

## Livestreaming

Broadcast the container display/audio to viewers.

### Video: WebRTC

Browser-friendly low-latency streaming.

**Start:**
```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"webrtc","id":"live-webrtc"}' | jq
```

**Response:**
```json
{
  "id": "live-webrtc",
  "mode": "webrtc",
  "ingest_url": "",
  "webrtc_offer_url": "/stream/webrtc/offer",
  "is_streaming": true,
  "started_at": "2025-12-10T12:00:00Z"
}
```

**Connect viewer (browser):**
```javascript
const pc = new RTCPeerConnection();
pc.ontrack = e => { document.getElementById('video').srcObject = e.streams[0]; };

const offer = await pc.createOffer({ offerToReceiveVideo: true, offerToReceiveAudio: true });
await pc.setLocalDescription(offer);

const res = await fetch('http://localhost:444/stream/webrtc/offer', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ sdp: pc.localDescription.sdp, id: 'live-webrtc' })
});
const { sdp } = await res.json();
await pc.setRemoteDescription({ type: 'answer', sdp });
```

**Real-time factor**: ~100-200ms glass-to-glass latency.

---

### Audio: WebSocket

MPEG-TS audio broadcast over WebSocket.

**Start:**
```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"socket","id":"audio-stream"}' | jq
```

**Response:**
```json
{
  "id": "audio-stream",
  "mode": "socket",
  "websocket_url": "/stream/socket/audio-stream",
  "is_streaming": true,
  "started_at": "2025-12-10T12:00:00Z"
}
```

**Consume:**
```javascript
const ws = new WebSocket('ws://localhost:444/stream/socket/audio-stream');
const chunks = [];
ws.onmessage = e => chunks.push(e.data);
```

**Capture to file:**
```bash
node --input-type=module - <<'NODE'
import fs from 'node:fs';
import WebSocket from 'ws';
const ws = new WebSocket('ws://localhost:444/stream/socket/audio-stream');
const out = fs.createWriteStream('audio_capture.ts');
ws.on('message', chunk => out.write(chunk));
ws.on('close', () => out.end());
NODE
```

**Real-time factor**: Chunks arrive as captured. No buffering.

---

### RTMP: Local Server

Internal RTMP server for local playback.

**Start:**
```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"internal"}' | jq
```

**Response:**
```json
{
  "id": "default",
  "mode": "internal",
  "ingest_url": "rtmp://localhost:1935/live/default",
  "playback_url": "rtmp://localhost:1935/live/default",
  "is_streaming": true,
  "started_at": "2025-12-10T12:00:00Z"
}
```

**Play with ffplay:**
```bash
ffplay -fflags nobuffer -i rtmp://localhost:1935/live/default
```

---

### RTMP: Remote Push

Push to external RTMP/RTMPS endpoint.

**Start:**
```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"remote","target_url":"rtmp://live.example.com/app/stream-key"}' | jq
```

**Response:**
```json
{
  "id": "default",
  "mode": "remote",
  "ingest_url": "rtmp://live.example.com/app/stream-key",
  "is_streaming": true,
  "started_at": "2025-12-10T12:00:00Z"
}
```

---

### Stream Control

**Stop:**
```bash
curl -X POST http://localhost:444/stream/stop
# or with id:
curl -X POST http://localhost:444/stream/stop -H "Content-Type: application/json" -d '{"id":"live-webrtc"}'
```

**List active:**
```bash
curl -s http://localhost:444/stream/list | jq
```

**Response:**
```json
[
  {
    "id": "live-webrtc",
    "mode": "webrtc",
    "is_streaming": true,
    "started_at": "2025-12-10T12:00:00Z"
  }
]
```

---

# Part 2: API Reference

## Virtual Inputs

### POST `/input/devices/virtual/configure`

Configure virtual webcam and microphone inputs.

**Request:**
```json
{
  "video": {
    "type": "socket|webrtc|stream|file",
    "url": "string (for stream/file types)",
    "format": "mpegts|ivf (for socket/webrtc)",
    "width": 1280,
    "height": 720,
    "frame_rate": 30
  },
  "audio": {
    "type": "socket|webrtc|stream|file",
    "url": "string (for stream/file types)",
    "format": "mp3|opus (for socket/webrtc)",
    "destination": "microphone|speaker"
  },
  "start_paused": false
}
```

**Response:** `VirtualInputsStatus`

---

### POST `/input/devices/virtual/webrtc/offer`

Exchange SDP for WebRTC ingest session.

**Request:**
```json
{
  "sdp": "v=0\r\no=- ..."
}
```

**Response:**
```json
{
  "sdp": "v=0\r\no=- ..."
}
```

---

### WebSocket `/input/devices/virtual/socket/video`

Ingest MPEG-TS video chunks.

**Protocol:** Binary WebSocket frames
**Format:** MPEG-TS with MPEG-1 video codec
**Behavior:** Write chunks continuously. Connection stays open. No chunks = no video on feed.

---

### WebSocket `/input/devices/virtual/socket/audio`

Ingest MP3 audio chunks.

**Protocol:** Binary WebSocket frames
**Format:** MP3
**Behavior:** Writes to virtual mic (default) or speaker based on configured `destination`.

---

### GET `/input/devices/virtual/feed`

HTML page displaying the virtual video feed.

**Query params:**
- `fit`: CSS object-fit (`cover`, `contain`, `fill`)
- `source`: Override source URL

---

### GET `/input/devices/virtual/feed/socket/info`

Discover WebSocket feed endpoint.

**Response:**
```json
{
  "url": "ws://localhost:10001/input/devices/virtual/feed/socket",
  "format": "mpegts"
}
```

---

### WebSocket `/input/devices/virtual/feed/socket`

Mirror of virtual video feed for external consumption.

**Protocol:** Binary WebSocket frames
**First message:** Text frame with format hint (`mpegts` or `ivf`)

---

### POST `/input/devices/virtual/pause`

Pause with black frames and silence.

---

### POST `/input/devices/virtual/resume`

Resume live media playback.

---

### POST `/input/devices/virtual/stop`

Stop pipelines and release resources.

---

### GET `/input/devices/virtual/status`

Current virtual input status.

**Response:** `VirtualInputsStatus`

---

## Livestreaming

### POST `/stream/start`

Start live streaming.

**Request:**
```json
{
  "id": "stream-id",
  "mode": "internal|remote|webrtc|socket",
  "target_url": "rtmp://... (for remote mode)",
  "framerate": 15
}
```

**Response:** `StreamInfo`

---

### POST `/stream/stop`

Stop a stream.

**Request:**
```json
{
  "id": "stream-id"
}
```

---

### GET `/stream/list`

List active streams.

**Response:** `StreamInfo[]`

---

### POST `/stream/webrtc/offer`

Exchange SDP for WebRTC playback.

**Request:**
```json
{
  "id": "stream-id",
  "sdp": "v=0\r\no=- ..."
}
```

**Response:**
```json
{
  "sdp": "v=0\r\no=- ..."
}
```

---

### WebSocket `/stream/socket/{id}`

MPEG-TS broadcast stream.

**Protocol:** Binary WebSocket frames
**Format:** MPEG-TS

---

## Types

### VirtualInputsStatus
```json
{
  "state": "idle|running|paused",
  "mode": "device|virtual-file",
  "video_device": "/dev/video0",
  "audio_sink": "audio_input",
  "microphone_source": "audio_input.monitor",
  "video_file": "/path/to/y4m",
  "audio_file": "/path/to/wav",
  "video": { "type": "...", "width": 1280, "height": 720 },
  "audio": { "type": "...", "destination": "microphone" },
  "started_at": "2025-12-10T12:00:00Z",
  "last_error": null,
  "ingest": {
    "video": { "protocol": "socket", "format": "mpegts", "url": "ws://..." },
    "audio": { "protocol": "socket", "format": "mp3", "destination": "microphone", "url": "ws://..." }
  }
}
```

### StreamInfo
```json
{
  "id": "stream-id",
  "mode": "internal|remote|webrtc|socket",
  "ingest_url": "rtmp://...",
  "playback_url": "rtmp://...",
  "secure_playback_url": "rtmps://...",
  "websocket_url": "/stream/socket/id",
  "webrtc_offer_url": "/stream/webrtc/offer",
  "is_streaming": true,
  "started_at": "2025-12-10T12:00:00Z"
}
```

### VirtualInputType
`stream | file | socket | webrtc`

### VirtualInputAudioDestination
`microphone | speaker`

---

## Real-time Behavior Summary

| Feature | Latency | Buffering | Refresh Behavior |
|---------|---------|-----------|------------------|
| WebRTC Virtual Input | ~50-150ms | None | Shows current frame |
| WebSocket Virtual Input | ~100-200ms | None | Shows current frame |
| WebRTC Livestream | ~100-200ms | None | Reconnects to live |
| WebSocket Livestream | ~100-200ms | None | Receives current chunks |
| RTMP Internal | ~500ms-1s | Minimal | ffplay rebuffers |
| RTMP Remote | Network dependent | Endpoint dependent | Endpoint dependent |

**Key behaviors:**
- No caching/replay of past frames
- Feed shows "Loading..." when no chunks arriving
- Page refresh shows current state only
- Streams stay open when idle, resume when chunks arrive
