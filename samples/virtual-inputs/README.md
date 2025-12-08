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
Use the bundled chunk sender to stream TS or MP4 video (and MP3 audio) in real chunks and keep the sockets alive:
```bash
npm install ws
node samples/virtual-inputs/ws_chunk_ingest.js
```
- Defaults: TS video + MP3 audio. Set `VIDEO_FORMAT=mp4` to use the MP4 sample or override `VIDEO_FILE/AUDIO_FILE`, `VIRTUAL_INPUT_HOST`, or `CHUNK_DELAY_MS`.
- Reconfigure the ingest to match when switching formats, e.g. set `"format": "mp4"` for the video source before sending MP4 chunks.

Open the live preview while the sockets run: `http://localhost:444/input/devices/virtual/feed?fit=cover`  
Discover the preview websocket URL/format: `curl http://localhost:444/input/devices/virtual/feed/socket/info | jq`
To sanity-check the mirrored feed directly, capture it to disk with `node samples/virtual-inputs/feed_capture.js` (override `VIRTUAL_INPUT_HOST` or `FEED_CAPTURE_FILE` as needed).

## WebRTC ingest (Python)
Prepare the ingest endpoints for WebRTC (both tracks stay on the same transport):
```bash
curl -s http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{"video":{"type":"webrtc"},"audio":{"type":"webrtc"}}' | jq
```
Use the bundled Python helper (keeps everything under `samples/virtual-inputs`, installs `uv`, and uses `MediaPlayer` from `aiortc.contrib.media`):
```bash
cd samples/virtual-inputs
sh run_webrtc.sh
```
If your working directory is mounted with `noexec`, the `sh` prefix avoids execution errors. The script defaults to a venv in `/tmp/kernel-virtual-inputs-webrtc-venv` (override with `VENV_PATH=...`) so native libraries can be loaded even on `noexec` workspaces.
Override the API target with `API_URL=http://localhost:444/input/devices/virtual/webrtc/offer sh run_webrtc.sh` if needed.
When the WebRTC negotiation completes, reload `/input/devices/virtual/feed` to watch the mirrored stream.

## Pause/stop helpers
- Pause the pipeline (sends black frames/silence): `curl -X POST http://localhost:444/input/devices/virtual/pause`
- Resume live media: `curl -X POST http://localhost:444/input/devices/virtual/resume`
- Stop and reset everything (also clears the preview websocket): `curl -X POST http://localhost:444/input/devices/virtual/stop`
