# Livestream samples

Practical calls for the livestream API grouped by mode. All examples assume the API is reachable on `http://localhost:10001` from the host.

## Internal RTMP preview
1. Start the internal broadcaster (auto-picks the default display/framerate):
   ```bash
   curl -s http://localhost:10001/stream/start \
     -H "Content-Type: application/json" \
     -d '{"mode":"internal"}' | jq
   ```
   The response includes `ingest_url` and `playback_url` values such as `rtmp://localhost:1935/live/default`.
2. Open the playback URL locally with `ffplay` for a low-latency check:
   ```bash
   ffplay -fflags nobuffer -i rtmp://localhost:1935/live/default
   ```
3. When done, stop the stream:
   ```bash
   curl -X POST http://localhost:10001/stream/stop
   ```

## Push to a remote RTMP/RTMPS target
Use any RTMP-ready endpoint (replace the URL with your real upstream target):
```bash
curl -s http://localhost:10001/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"remote","target_url":"rtmp://example.com/live/default"}' | jq
```
The server logs show the ffmpeg push; stop with `/stream/stop` when finished.

## WebSocket MPEG-TS broadcast
Stream the display as MPEG-TS chunks over a websocket for custom consumers.
1. Start the socket mode streamer with an ID:
   ```bash
   curl -s http://localhost:10001/stream/start \
     -H "Content-Type: application/json" \
     -d '{"mode":"socket","id":"live-ts"}' | jq
   ```
2. Consume the websocket at `/stream/socket/live-ts` with a small Node.js snippet:
   ```bash
   node - <<'NODE'
   import fs from 'node:fs';
   import WebSocket from 'ws';
   const ws = new WebSocket('ws://localhost:10001/stream/socket/live-ts');
   const out = fs.createWriteStream('capture.ts');
   ws.on('message', chunk => out.write(chunk));
   ws.on('close', () => out.end());
   NODE
   ```
3. Inspect the captured `capture.ts` using `ffprobe` or `ffplay`.

## WebRTC playback
Expose the livestream through WebRTC for browser-friendly consumption:
```bash
curl -s http://localhost:10001/stream/start \
  -H "Content-Type: application/json" \
  -d '{"mode":"webrtc","id":"webrtc-live"}' | jq
```
Then send an SDP offer to `/stream/webrtc/offer` (see `server/openapi.yaml` for the request schema). The answer SDP can be set as the remote description in your WebRTC player.
