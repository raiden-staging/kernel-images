````xml
<file path="README.md">
# Kernel Operator API (Go)

A lightweight, single-binary HTTP API that exposes local OS capabilities for automation and testing: filesystem access, process control, keyboard/mouse input (X11), screenshots/recording, simple streaming (FFmpeg), SSE event pipes, metrics snapshots, basic network forwarding &amp; request interception stubs, clipboard I/O, and a few browser/extension and OS locale helpers.

> ⚠️ **Security:** There is **no auth** and CORS is wide open by design (dev/test parity). **Do not** expose this service to untrusted networks. Run in a sandboxed VM or behind a firewall.

---

## Requirements

- **Go** 1.21+
- **Linux** host (X11 for input APIs; Wayland-only is partially supported for clipboard/screenshot)
- Binaries used by various routes (install as needed):
  - `ffmpeg` (screenshots fallback, recording, streaming)
  - `xdotool` (mouse/keyboard input; requires X11/Xwayland)
  - `xclip` (X11 clipboard)
  - `wl-copy`, `wl-paste` (Wayland clipboard)
  - `grim` (Wayland screenshots; optional, used if present)
  - `journalctl`, `tail`, `base64`, `bash`

### Environment variables (optional)

- `PORT` (default `10001`)
- `DATA_DIR` (default `/tmp/kernel-operator-api/data`)
- `FFMPEG_BIN` (default `/usr/bin/ffmpeg`)
- `XDOTOOL_BIN` (default `/usr/bin/xdotool`)
- `DISPLAY` (default `:0`)
- `PULSE_SOURCE` (default `default`)
- `TZ`, `LANG`, `XKB_DEFAULT_LAYOUT`
- `DEBUG_LOGS=1` (log exec invocations and stdio)

---

## Build &amp; Run

```bash
# 1) build
go build -o bin/operator-api ./cmd/operator-api

# 2) run (from repo root)
./bin/operator-api

# or directly:
go run ./cmd/operator-api
````

The server prints environment variables at startup and listens on `:$PORT` (default `:10001`).

---

## Quick smoke test

```bash
curl -s http://localhost:10001/health | jq
# { "status":"ok", "uptime_sec": N }
```
