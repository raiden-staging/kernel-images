# Kernel Images Server

A REST API server to start, stop, and download screen recordings.

## 🛠️ Prerequisites

### Required Software

- **Go 1.24.3+** - Programming language runtime
- **ffmpeg** - Video recording engine
  - macOS: `brew install ffmpeg`
  - Linux: `sudo apt install ffmpeg` or `sudo yum install ffmpeg`
- **pnpm** - For OpenAPI code generation
  - `npm install -g pnpm`

### System Requirements

- **macOS**: Uses AVFoundation for screen capture
- **Linux**: Uses X11 for screen capture
- **Windows**: Not currently supported

## 🚀 Quick Start

### Running the Server

```bash
make dev
```

The server will start on port 10001 by default and log its configuration.

#### Example use

```bash
# 1. Start a new recording
curl http://localhost:10001/recording/start -d {}

# (recording in progress)

# 2. Stop recording
curl http://localhost:10001/recording/stop -d {}

# 3. Download the recorded file
curl http://localhost:10001/recording/download --output recording.mp4
```

### ⚙️ Configuration

Configure the server using environment variables:

| Variable       | Default   | Description                                 |
| -------------- | --------- | ------------------------------------------- |
| `PORT`         | `10001`   | HTTP server port                            |
| `FRAME_RATE`   | `10`      | Default recording framerate (fps)           |
| `DISPLAY_NUM`  | `1`       | Display/screen number to capture            |
| `MAX_SIZE_MB`  | `500`     | Default maximum file size (MB)              |
| `OUTPUT_DIR`   | `.`       | Directory to save recordings                |
| `FFMPEG_PATH`  | `ffmpeg`  | Path to the ffmpeg binary                   |
| `RTMP_LISTEN_ADDR` | `:1935` | RTMP listen address for the internal server |
| `RTMPS_LISTEN_ADDR` | `:1936` | RTMPS listen address (self-signed certs are generated when no cert/key is provided) |
| `RTMPS_CERT_PATH` | _(empty)_ | TLS certificate for RTMPS (requires `RTMPS_KEY_PATH` when set) |
| `RTMPS_KEY_PATH` | _(empty)_ | TLS private key for RTMPS (requires `RTMPS_CERT_PATH` when set) |

#### Example Configuration

```bash
export PORT=8080
export FRAME_RATE=30
export MAX_SIZE_MB=1000
export OUTPUT_DIR=/tmp/recordings
./bin/api
```

### API Documentation

- **YAML Spec**: `GET /spec.yaml`
- **JSON Spec**: `GET /spec.json`

## 📡 Livestreaming

Use `/stream/start` to broadcast the display either to the internal RTMP(S) server (ports 1935/1936 exposed by the Docker/unikernel runners) or to a remote RTMP/RTMPS endpoint.

- Internal RTMP server (returns ingest + playback URLs):

```bash
curl http://localhost:10001/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"internal"}'
```

- Push to a remote RTMP target:

```bash
curl http://localhost:10001/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"remote","target_url":"rtmp://example.com/live/default"}'
```

Stop or enumerate streams:

```bash
curl -X POST http://localhost:10001/stream/stop
curl http://localhost:10001/stream/list
```

## 🎥 Virtual Media Inputs

The server exposes virtual webcam/microphone control under `/input/devices/virtual/*`. Media is injected into a v4l2loopback camera when available, or piped into Chromium’s virtual capture flags so Chromium/Neko see OS-level devices.

### Defaults

Environment variables control the virtual devices and output shape:

| Variable                          | Default          | Description                                                     |
| --------------------------------- | ---------------- | --------------------------------------------------------------- |
| `VIRTUAL_INPUT_VIDEO_DEVICE`      | `/dev/video20`   | v4l2loopback device used for the virtual camera                 |
| `VIRTUAL_INPUT_AUDIO_SINK`        | `audio_input`    | PulseAudio sink that receives injected audio                    |
| `VIRTUAL_INPUT_MICROPHONE_SOURCE` | `microphone`     | PulseAudio source clients should select as the microphone       |
| `VIRTUAL_INPUT_WIDTH`             | `1280`           | Output width for the virtual camera                             |
| `VIRTUAL_INPUT_HEIGHT`            | `720`            | Output height for the virtual camera                            |
| `VIRTUAL_INPUT_FRAME_RATE`        | `30`             | Output frame rate for the virtual camera                        |

If the host kernel cannot load `v4l2loopback`, the server automatically falls back to streaming into Chromium’s virtual capture flags using Y4M/WAV pipes under `/tmp/virtual-inputs/*` and restarts Chromium to pick them up.

### Preview the virtual feed

Open `http://localhost:10001/input/devices/virtual/feed` for a fullscreen, muted preview of the configured video input. Optional query params:

- `fit`: CSS object-fit mode (default `cover`)
- `source`: override the detected video source (HTTP/HLS/WebSocket/WebRTC)
- `GET /input/devices/virtual/feed/socket/info`: helper to discover the websocket mirror URL and expected format when the feed is sourced from socket/WebRTC ingest

### Useful Requests

Configure a virtual webcam + mic from mixed sources:

```bash
curl -X POST http://localhost:10001/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {
      "type": "stream",
      "url": "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8",
      "width": 1280,
      "height": 720,
      "frame_rate": 30
    },
    "audio": {"type": "file", "url": "https://download.samplelib.com/mp3/sample-15s.mp3"}
  }'
```

Pause/resume or stop the feed:

```bash
curl -X POST http://localhost:10001/input/devices/virtual/pause
curl -X POST http://localhost:10001/input/devices/virtual/resume
curl -X POST http://localhost:10001/input/devices/virtual/stop
```

Check the active state and sources:

```bash
curl http://localhost:10001/input/devices/virtual/status | jq
```

Use `start_paused: true` in the configure body to begin with black video/silence, then resume when ready to expose the media.

## 🔧 Development

### Code Generation

The server uses OpenAPI code generation. After modifying `openapi.yaml`:

```bash
make oapi-generate
```

## 🧪 Testing

### Running Tests

```bash
make test
```
