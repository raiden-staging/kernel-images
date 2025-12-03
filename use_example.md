# Virtual Input API – Quick Curl Flow

Endpoints live under `/input/devices/virtual/*`. These examples use the sample media from the task files and assume the API is reachable on `http://localhost:444`.

## 1) Status (baseline)
```bash
curl -s http://localhost:444/input/devices/virtual/status | jq
# Expect: state=idle, mode=device, video_device=/dev/video20, audio_sink=audio_input
```

## 2) Configure (paused start)
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
# Expect: 200 JSON, state=paused, mode=virtual-file, video_file/audio_file under /tmp/virtual-inputs, Chromium flags updated
```

## 3) Resume (start live media)
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/resume
# Expect: 200 JSON, state=running, one ffmpeg pipeline feeding video.y4m/audio.wav
```

## 4) Pause (should stop live pipeline)
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/pause
# Expect: 200 JSON, state=paused, live ffmpeg terminated (only black/silence if any)
```

## 5) Stop (tear down all pipelines)
```bash
curl -s -w '\n%{http_code}\n' -X POST http://localhost:444/input/devices/virtual/stop
# Expect: 200 JSON, state=idle, mode=device, no ffmpeg processes left
```

## 6) Confirm no ffmpeg remains (container shell)
```bash
docker exec chromium-headful-test pgrep -fl ffmpeg
# Expect: no output
```
