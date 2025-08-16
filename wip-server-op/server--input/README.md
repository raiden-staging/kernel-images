# Input API Server

A REST API server that implements `/input/*` endpoints for input simulation (keyboard/mouse) and window management. This server provides a comprehensive set of endpoints for controlling input devices and managing windows in an X11 environment.

## 🛠️ Prerequisites

### Required Software

- **Go 1.24.3+** - Programming language runtime
- **xdotool** - Tool for X11 input simulation
  - Linux: `sudo apt install xdotool` or `sudo yum install xdotool`
- **X11 server** - Display server for Linux

### System Requirements

- **Linux**: Uses X11 for input simulation and window management
- **macOS/Windows**: Not currently supported

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

#### Additional routes

```bash
# Input operations

# Mouse operations
# | POST /input/mouse/move - Move mouse to absolute coordinates
curl -X POST -H "Content-Type: application/json" \
  --data '{"x": 500, "y": 500}' \
  http://localhost:10001/input/mouse/move
# Response: {"ok":true}

# | GET /input/mouse/location - Get current mouse location
curl http://localhost:10001/input/mouse/location
# Response: {"x":500,"y":500,"screen":0,"window":"60817493"}

# | POST /input/mouse/click - Click mouse button
curl -X POST -H "Content-Type: application/json" \
  --data '{"button": "left", "count": 1}' \
  http://localhost:10001/input/mouse/click
# Response: {"ok":true}

# | POST /input/mouse/scroll - Scroll mouse wheel
curl -X POST -H "Content-Type: application/json" \
  --data '{"dx": 0, "dy": -120}' \
  http://localhost:10001/input/mouse/scroll
# Response: {"ok":true}

# Keyboard operations
# | POST /input/keyboard/type - Type text
curl -X POST -H "Content-Type: application/json" \
  --data '{"text": "Hello, World!", "wpm": 300, "enter": true}' \
  http://localhost:10001/input/keyboard/type
# Response: {"ok":true}

# | POST /input/keyboard/key - Send key presses
curl -X POST -H "Content-Type: application/json" \
  --data '{"keys": ["ctrl", "a"]}' \
  http://localhost:10001/input/keyboard/key
# Response: {"ok":true}

# Window operations
# | GET /input/window/active - Get active window
curl http://localhost:10001/input/window/active
# Response: {"wid":"60817493"}

# | POST /input/window/name - Get window name
curl -X POST -H "Content-Type: application/json" \
  --data '{"wid": "60817493"}' \
  http://localhost:10001/input/window/name
# Response: {"wid":"60817493","name":"New Tab - Google Chrome"}

# | POST /input/window/close - Close window by match
curl -X POST -H "Content-Type: application/json" \
  --data '{"match": {"title_contains": "New Tab"}}' \
  http://localhost:10001/input/window/close
# Response: {"ok":true,"wid":"60817493","windowIds":["60817493"]}

# | GET /input/display/geometry - Get display geometry
curl http://localhost:10001/input/display/geometry
# Response: {"width":1536,"height":776}

# Legacy endpoints (for compatibility)
# | POST /computer/move_mouse - Move mouse cursor (maps to /input/mouse/move)
curl -X POST -H "Content-Type: application/json" \
  --data '{"x": 100, "y": 100}' \
  http://localhost:10001/computer/move_mouse
# Response: {"ok":true}

# | POST /computer/click_mouse - Click mouse (maps to /input/mouse/click)
curl -X POST -H "Content-Type: application/json" \
  --data '{"button": "left", "x": 100, "y": 100}' \
  http://localhost:10001/computer/click_mouse
# Response: {"ok":true}
```

### ⚙️ Configuration

Configure the server using environment variables:

| Variable       | Default   | Description                                 |
| -------------- | --------- | ------------------------------------------- |
| `PORT`         | `10001`   | HTTP server port                            |
| `FRAME_RATE`   | `10`      | Default recording framerate (fps)           |
| `DISPLAY_NUM`  | `1`       | Display/screen number to capture            |
| `DISPLAY`      | `:1`      | X11 display to use for input operations     |
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

### Testing Input API

For testing the input API in development, use display 20:

```bash
# Run server with display 20
DISPLAY=:20 ./bin/api

# Test with the provided script
./scripts/test_endpoints.sh
```

NOTE: Always use DISPLAY=:20 for testing in development environment. In production, the default of :1 should be used.
