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

## 🎥 Virtual Media Inputs

The server exposes virtual webcam/microphone control under `/virtual_inputs/*`. Media is injected into a v4l2loopback camera and a PulseAudio sink so Chromium (and Neko) see real devices at the OS level.

### Defaults

Environment variables control the virtual devices and output shape:

| Variable                         | Default          | Description                                                     |
| -------------------------------- | ---------------- | --------------------------------------------------------------- |
| `VIRTUAL_INPUT_VIDEO_DEVICE`     | `/dev/video20`   | v4l2loopback device used for the virtual camera                 |
| `VIRTUAL_INPUT_AUDIO_SINK`       | `audio_input`    | PulseAudio sink that receives injected audio                    |
| `VIRTUAL_INPUT_MICROPHONE_SOURCE`| `microphone`     | PulseAudio source clients should select as the microphone       |
| `VIRTUAL_INPUT_WIDTH`            | `1280`           | Output width for the virtual camera                             |
| `VIRTUAL_INPUT_HEIGHT`           | `720`            | Output height for the virtual camera                            |
| `VIRTUAL_INPUT_FRAME_RATE`       | `30`             | Output frame rate for the virtual camera                        |

### Useful Requests

Configure a virtual webcam + mic from mixed sources (looping the audio file):

```bash
curl -X POST http://localhost:10001/virtual_inputs/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "stream", "url": "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"},
    "audio": {"type": "file", "url": "https://download.samplelib.com/mp3/sample-15s.mp3", "loop": true},
    "width": 1280,
    "height": 720,
    "frame_rate": 30
  }'
```

Pause/resume or stop the feed:

```bash
curl -X POST http://localhost:10001/virtual_inputs/pause
curl -X POST http://localhost:10001/virtual_inputs/resume
curl -X POST http://localhost:10001/virtual_inputs/stop
```

Check the active state and sources:

```bash
curl http://localhost:10001/virtual_inputs/status | jq
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
