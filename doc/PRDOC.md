# Virtual Inputs & Livestreams - PR Documentation

This document covers the virtual input devices and livestream broadcasting features. API is accessible at `http://localhost:444` from host (port `10001` inside container).

---

## Part 1: Utilities & Use Cases

### 1.1 Virtual Video Input - WebSocket Feed

Real-time video chunks via WebSocket. Uses MPEG-1 video in MPEG-TS container for JSMpeg playback.

#### Configure WebSocket Video Input

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {
      "type": "socket",
      "format": "mpegts",
      "width": 1280,
      "height": 720,
      "frame_rate": 30
    }
  }' | jq
```

**Expected Response:**
```json
{
  "state": "running",
  "mode": "virtual-file",
  "video_device": "/dev/video0",
  "audio_sink": "audio_input",
  "microphone_source": "virtual_mic",
  "video": {
    "type": "socket",
    "format": "mpegts",
    "width": 1280,
    "height": 720,
    "frame_rate": 30
  },
  "ingest": {
    "video": {
      "protocol": "socket",
      "format": "mpegts",
      "url": "ws://localhost:10001/input/devices/virtual/socket/video"
    }
  }
}
```

#### Encode Source Video to MPEG-1

```bash
# Convert any video to MPEG-1 (required for JSMpeg)
ffmpeg -i input.mp4 -c:v mpeg1video -b:v 1500k -r 25 -f mpegts output.ts
```

#### Feed Video Chunks (Node.js)

```javascript
import { createReadStream } from 'node:fs';
import WebSocket from 'ws';

const ws = new WebSocket('ws://localhost:444/input/devices/virtual/socket/video');
const delay = ms => new Promise(r => setTimeout(r, ms));

ws.on('open', async () => {
  for await (const chunk of createReadStream('video.ts', { highWaterMark: 64*1024 })) {
    ws.send(chunk);
    await delay(35); // ~realtime pacing
  }
  console.log('Streaming... socket left open for more chunks');
});
```

#### Real-time Behavior

- Feed page shows video only when chunks arrive
- Refresh = no cached replay; shows "Loading..." until new chunks
- Stop sending = black screen; resume = video resumes
- This is **true real-time**: no buffering of past data

#### Preview Feed

Open in browser: `http://localhost:444/input/devices/virtual/feed?fit=cover`

---

### 1.2 Virtual Video Input - WebRTC Feed

Real-time video via WebRTC (VP8/VP9 in IVF format internally).

#### Configure WebRTC Video Input

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"video": {"type": "webrtc"}}' | jq
```

**Expected Response:**
```json
{
  "state": "running",
  "mode": "virtual-file",
  "video": {"type": "webrtc"},
  "ingest": {
    "video": {
      "protocol": "webrtc",
      "format": "ivf",
      "url": "http://localhost:10001/input/devices/virtual/webrtc/offer"
    }
  }
}
```

#### Send Video via WebRTC (Python)

```python
import asyncio, aiohttp
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer

async def main():
    pc = RTCPeerConnection()
    player = MediaPlayer("video.mp4")
    if player.video:
        pc.addTrack(player.video)

    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as s:
        resp = await s.post(
            "http://localhost:444/input/devices/virtual/webrtc/offer",
            json={"sdp": pc.localDescription.sdp}
        )
        answer = await resp.json()

    await pc.setRemoteDescription(
        RTCSessionDescription(sdp=answer["sdp"], type="answer")
    )
    print("Streaming...")
    await asyncio.Future()

asyncio.run(main())
```

#### Real-time Factor

- WebRTC provides lowest latency (~100-300ms typical)
- Feed page refreshes show current frame, not cached history
- Track stops = black screen; track resumes = video resumes

---

### 1.3 Virtual Audio Input - WebSocket Feed

Real-time audio chunks via WebSocket (MP3 format).

#### Configure WebSocket Audio Input (to Virtual Mic)

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "audio": {
      "type": "socket",
      "format": "mp3",
      "destination": "microphone"
    }
  }' | jq
```

**Expected Response:**
```json
{
  "state": "running",
  "audio_sink": "audio_input",
  "microphone_source": "virtual_mic",
  "audio": {
    "type": "socket",
    "format": "mp3",
    "destination": "microphone"
  },
  "ingest": {
    "audio": {
      "protocol": "socket",
      "format": "mp3",
      "destination": "microphone",
      "url": "ws://localhost:10001/input/devices/virtual/socket/audio"
    }
  }
}
```

#### Audio Destinations

| Destination | PulseAudio Sink | Use Case |
|------------|-----------------|----------|
| `microphone` (default) | `audio_input` | Virtual mic for apps reading mic input |
| `speaker` | `audio_output` | Monitor/playback through container audio |

#### Feed Audio Chunks (Node.js)

```javascript
import { createReadStream } from 'node:fs';
import WebSocket from 'ws';

const ws = new WebSocket('ws://localhost:444/input/devices/virtual/socket/audio');
const delay = ms => new Promise(r => setTimeout(r, ms));

ws.on('open', async () => {
  for await (const chunk of createReadStream('audio.mp3', { highWaterMark: 16*1024 })) {
    ws.send(chunk);
    await delay(50);
  }
  console.log('Audio streaming... socket open for more');
});
```

#### Example Logs (Real-time Audio Ingest)

```
[virtual-input] audio socket connected
[virtual-input] audio chunk received: 16384 bytes
[virtual-input] routing to microphone (audio_input sink)
[virtual-input] audio chunk received: 16384 bytes
[virtual-input] audio chunk received: 8192 bytes
[virtual-input] audio ingest idle, waiting for chunks...
[virtual-input] audio chunk received: 16384 bytes
[virtual-input] routing resumed to microphone
```

---

### 1.4 Virtual Audio Input - WebRTC Feed

Real-time audio via WebRTC (Opus codec).

#### Configure WebRTC Audio Input

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "audio": {"type": "webrtc", "destination": "microphone"}
  }' | jq
```

#### Route to Speaker Instead

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "audio": {"type": "webrtc", "destination": "speaker"}
  }' | jq
```

#### Send Audio via WebRTC (Python)

```python
import asyncio, aiohttp
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer

async def main():
    pc = RTCPeerConnection()
    player = MediaPlayer("audio.mp3")
    if player.audio:
        pc.addTrack(player.audio)

    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as s:
        resp = await s.post(
            "http://localhost:444/input/devices/virtual/webrtc/offer",
            json={"sdp": pc.localDescription.sdp}
        )
        answer = await resp.json()

    await pc.setRemoteDescription(
        RTCSessionDescription(sdp=answer["sdp"], type="answer")
    )
    await asyncio.Future()

asyncio.run(main())
```

---

### 1.5 Combined Virtual Input (Video + Audio)

#### WebSocket Video + Audio

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "socket", "format": "mpegts", "width": 1280, "height": 720},
    "audio": {"type": "socket", "format": "mp3"}
  }' | jq
```

Then feed both sockets simultaneously:
- Video: `ws://localhost:444/input/devices/virtual/socket/video`
- Audio: `ws://localhost:444/input/devices/virtual/socket/audio`

#### WebRTC Video + Audio

```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "webrtc"},
    "audio": {"type": "webrtc"}
  }' | jq
```

Both tracks use the same WebRTC peer connection.

---

### 1.6 Livestream - WebRTC Playback

Expose container display as WebRTC stream for browser consumption.

#### Start WebRTC Livestream

```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode": "webrtc", "id": "webrtc-live"}' | jq
```

**Expected Response:**
```json
{
  "id": "webrtc-live",
  "mode": "webrtc",
  "ingest_url": "",
  "webrtc_offer_url": "http://localhost:10001/stream/webrtc/offer",
  "is_streaming": true,
  "started_at": "2024-01-15T10:30:00Z"
}
```

#### Connect from Browser (JavaScript)

```javascript
const pc = new RTCPeerConnection();
pc.ontrack = e => {
  document.getElementById('video').srcObject = e.streams[0];
};

const offer = await pc.createOffer({ offerToReceiveVideo: true, offerToReceiveAudio: true });
await pc.setLocalDescription(offer);

const resp = await fetch('http://localhost:444/stream/webrtc/offer', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ id: 'webrtc-live', sdp: pc.localDescription.sdp })
});
const answer = await resp.json();
await pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
```

#### Real-time Factor

- Sub-second latency typical
- No buffering; frame drops on slow connections
- Ideal for live monitoring

---

### 1.7 Livestream - WebSocket Audio

Stream container audio output as MP3 chunks over WebSocket.

#### Start Socket Audio Livestream

```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode": "socket", "id": "audio-live"}' | jq
```

**Expected Response:**
```json
{
  "id": "audio-live",
  "mode": "socket",
  "ingest_url": "",
  "websocket_url": "ws://localhost:10001/stream/socket/audio-live",
  "is_streaming": true
}
```

#### Consume Audio Stream (Node.js)

```javascript
import WebSocket from 'ws';
import fs from 'node:fs';

const ws = new WebSocket('ws://localhost:444/stream/socket/audio-live');
const out = fs.createWriteStream('captured_audio.ts');

ws.on('message', chunk => out.write(chunk));
ws.on('close', () => out.end());
```

#### Example Logs (Audio Livestream)

```
[livestream] starting socket mode stream: audio-live
[livestream] capturing audio from pulse audio_output
[livestream] websocket client connected to audio-live
[livestream] streaming audio chunk: 4096 bytes
[livestream] streaming audio chunk: 4096 bytes
[livestream] client disconnected from audio-live
```

---

### 1.8 Livestream - RTMP (Local & Remote)

#### Internal RTMP Server

```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode": "internal"}' | jq
```

**Expected Response:**
```json
{
  "id": "default",
  "mode": "internal",
  "ingest_url": "rtmp://localhost:1935/live/default",
  "playback_url": "rtmp://localhost:1935/live/default",
  "is_streaming": true
}
```

#### Play with ffplay

```bash
ffplay -fflags nobuffer -i rtmp://localhost:1935/live/default
```

#### Push to Remote RTMP

```bash
curl -s http://localhost:444/stream/start \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "remote",
    "target_url": "rtmp://live.example.com/app/stream-key"
  }' | jq
```

---

### 1.9 Control Commands

#### Pause (Black Frames/Silence)

```bash
curl -X POST http://localhost:444/input/devices/virtual/pause
```

#### Resume

```bash
curl -X POST http://localhost:444/input/devices/virtual/resume
```

#### Stop All

```bash
curl -X POST http://localhost:444/input/devices/virtual/stop
```

#### Stop Livestream

```bash
curl -X POST http://localhost:444/stream/stop
```

---

## Part 2: API Reference

### Virtual Inputs

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/input/devices/virtual/configure` | POST | Configure video/audio virtual inputs |
| `/input/devices/virtual/status` | GET | Get current virtual input status |
| `/input/devices/virtual/pause` | POST | Pause with black frames/silence |
| `/input/devices/virtual/resume` | POST | Resume live media |
| `/input/devices/virtual/stop` | POST | Stop and release resources |
| `/input/devices/virtual/feed` | GET | HTML page for live preview |
| `/input/devices/virtual/feed/socket/info` | GET | WebSocket URL info for feed |
| `/input/devices/virtual/webrtc/offer` | POST | WebRTC SDP negotiation |
| `/input/devices/virtual/socket/video` | WS | WebSocket video ingest |
| `/input/devices/virtual/socket/audio` | WS | WebSocket audio ingest |

### Livestream

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/stream/start` | POST | Start livestream (internal/remote/webrtc/socket) |
| `/stream/stop` | POST | Stop livestream |
| `/stream/list` | GET | List active streams |
| `/stream/webrtc/offer` | POST | WebRTC SDP for livestream playback |
| `/stream/socket/{id}` | WS | WebSocket MPEG-TS stream |

---

### Request/Response Schemas

#### VirtualInputsRequest

```typescript
interface VirtualInputsRequest {
  video?: {
    type: "stream" | "file" | "socket" | "webrtc";
    url?: string;           // For stream/file types
    format?: string;        // "mpegts" for socket, "ivf" for webrtc
    width?: number;
    height?: number;
    frame_rate?: number;
  };
  audio?: {
    type: "stream" | "file" | "socket" | "webrtc";
    url?: string;
    format?: string;        // "mp3" for socket
    destination?: "microphone" | "speaker";  // Default: microphone
  };
  start_paused?: boolean;
}
```

#### VirtualInputsStatus

```typescript
interface VirtualInputsStatus {
  state: "idle" | "running" | "paused";
  mode: "device" | "virtual-file";
  video_device: string;
  audio_sink: string;
  microphone_source: string;
  video?: VirtualInputVideo;
  audio?: VirtualInputAudio;
  ingest?: {
    video?: { protocol: string; format: string; url: string; };
    audio?: { protocol: string; format: string; destination: string; url: string; };
  };
  started_at?: string;
  last_error?: string;
}
```

#### StartStreamRequest

```typescript
interface StartStreamRequest {
  id?: string;
  mode: "internal" | "remote" | "webrtc" | "socket";
  target_url?: string;      // Required for "remote" mode
  framerate?: number;       // 1-20 fps
}
```

#### StreamInfo

```typescript
interface StreamInfo {
  id: string;
  mode: "internal" | "remote" | "webrtc" | "socket";
  ingest_url: string;
  playback_url?: string;
  websocket_url?: string;
  webrtc_offer_url?: string;
  is_streaming: boolean;
  started_at: string;
}
```

---

### Video Encoding Notes

The feed page uses **JSMpeg** for WebSocket video playback, which requires **MPEG-1 video codec**.

#### Encoding Command

```bash
ffmpeg -i source.mp4 -c:v mpeg1video -b:v 1500k -r 25 -f mpegts output.ts
```

#### Parameters

| Parameter | Value | Notes |
|-----------|-------|-------|
| `-c:v mpeg1video` | MPEG-1 | Required for JSMpeg |
| `-b:v 1500k` | 1.5 Mbps | Adjust for quality/bandwidth |
| `-r 25` | 25 fps | Match source or reduce |
| `-f mpegts` | MPEG-TS | Container format for streaming |

---

### Audio Format Notes

#### WebSocket Audio Ingest

- **Format**: MP3 chunks
- **Chunk size**: 16-64 KB typical
- **Pacing**: ~50ms between chunks for real-time

#### WebRTC Audio

- **Codec**: Opus
- **Handled automatically** by WebRTC stack

---

### Real-time Behavior Summary

| Feature | Latency | Buffer | Refresh Behavior |
|---------|---------|--------|------------------|
| WebSocket Video | ~100-500ms | None | Shows "Loading..." until chunks arrive |
| WebRTC Video | ~100-300ms | Minimal | Current frame only |
| WebSocket Audio | ~50-200ms | None | Silence when idle |
| WebRTC Audio | ~50-150ms | Minimal | Silence when idle |
| RTMP Internal | ~1-3s | Some | Standard RTMP behavior |

**Key Principle**: No caching of past data. When chunks stop, output shows idle state. When chunks resume, output resumes from current data.
