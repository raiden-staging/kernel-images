# Virtual Input API Quick Test

Endpoints now live under `/input/devices/virtual/*`. The examples below assume the container is running with API on port `444` (per `run-docker.sh`) and use the provided sample sources.

## 1) Check status
```bash
curl -s http://localhost:444/input/devices/virtual/status | jq
# Expect: idle state, mode=device, video_device=/dev/video20, audio_sink=audio_input
```

## 2) Configure (pause on start)
```bash
curl -s -X POST http://localhost:444/input/devices/virtual/configure \
  -H "Content-Type: application/json" \
  -d '{
    "video": {"type": "stream", "url": "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"},
    "audio": {"type": "file", "url": "https://download.samplelib.com/mp3/sample-15s.mp3", "loop": true},
    "width": 640,
    "height": 360,
    "frame_rate": 25,
    "start_paused": true
  }' | jq
# Expect: 200 JSON with state=paused, mode=virtual-file, video_file/audio_file paths under /tmp/virtual-inputs
```

## 3) Resume
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/resume
# Expect: 200 JSON with state=running, mode=virtual-file, updated started_at; ffmpeg should be streaming
```

## 4) Pause again
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/pause
# Expect: 200 JSON with state=paused; ffmpeg pipeline should tear down or switch to black/silence only
```

## 5) Stop
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/stop
# Expect: 200 JSON with state=idle, mode=device; no ffmpeg processes remain
```

## 6) Verify no ffmpeg remains
```bash
docker exec chromium-headful-test pgrep -fl ffmpeg
# Expect: no output
```
