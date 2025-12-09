package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

func (s *ApiService) GetVirtualInputFeed(ctx context.Context, req oapi.GetVirtualInputFeedRequestObject) (oapi.GetVirtualInputFeedResponseObject, error) {
	fit := "cover"
	if req.Params.Fit != nil && strings.TrimSpace(*req.Params.Fit) != "" {
		fit = strings.TrimSpace(*req.Params.Fit)
	}

	var source string
	if req.Params.Source != nil {
		source = strings.TrimSpace(*req.Params.Source)
	}

	defaultFormat := ""
	if source == "" {
		status := s.virtualInputs.Status(ctx)
		if status.Ingest != nil && status.Ingest.Video != nil {
			ingest := status.Ingest.Video
			defaultFormat = ingest.Format
			if defaultFormat == "" {
				switch ingest.Protocol {
				case string(virtualinputs.SourceTypeSocket):
					defaultFormat = "mpegts"
				case string(virtualinputs.SourceTypeWebRTC):
					defaultFormat = "ivf"
				}
			}
			if ingest.Protocol == string(virtualinputs.SourceTypeSocket) || ingest.Protocol == string(virtualinputs.SourceTypeWebRTC) {
				source = "/input/devices/virtual/feed/socket"
			} else if ingest.Path != "" {
				source = ingest.Path
			}
		}

		if source == "" && status.Video != nil && status.Video.URL != "" {
			source = status.Video.URL
		}
	}

	page := renderVirtualFeedPage(fit, source, defaultFormat)
	return oapi.GetVirtualInputFeed200TexthtmlResponse{
		Body:          strings.NewReader(page),
		ContentLength: int64(len(page)),
	}, nil
}

func renderVirtualFeedPage(fit, source, format string) string {
	fit = html.EscapeString(strings.TrimSpace(fit))
	if fit == "" {
		fit = "cover"
	}
	sourceJSON, _ := json.Marshal(source)
	formatJSON, _ := json.Marshal(format)

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Virtual Input Feed</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;600&display=swap');
    :root {
      color-scheme: dark;
    }
    * { box-sizing: border-box; }
    body, html {
      margin: 0; padding: 0;
      width: 100%%; height: 100%%;
      background: radial-gradient(circle at 15%% 20%%, #111827, #05070d 50%%);
      color: #d9dee9;
      font-family: 'Space Grotesk', system-ui, -apple-system, sans-serif;
      overflow: hidden;
    }
    #overlay {
      position: fixed;
      inset: 0;
      display: flex;
      align-items: center;
      justify-content: center;
      pointer-events: none;
      background: linear-gradient(120deg, rgba(16,24,40,0.6), rgba(5,7,13,0.75));
      backdrop-filter: blur(4px);
      transition: opacity 0.4s ease;
    }
    #overlay.hidden { opacity: 0; }
    #status {
      padding: 14px 18px;
      border-radius: 12px;
      background: rgba(20,24,35,0.8);
      border: 1px solid rgba(255,255,255,0.08);
      box-shadow: 0 12px 32px rgba(0,0,0,0.35);
      font-size: 15px;
      line-height: 1.4;
      text-align: center;
    }
    .frame {
      position: absolute;
      inset: 0;
      width: 100vw;
      height: 100vh;
      object-fit: %s;
      background: #000;
    }
    canvas.frame { background: #000; }
    .hidden { display: none; }
  </style>
  <script src="https://cdn.jsdelivr.net/npm/hls.js@1.5.9/dist/hls.min.js" crossorigin="anonymous"></script>
</head>
<body>
  <video id="video" class="frame" autoplay muted playsinline></video>
  <canvas id="canvas" class="frame hidden"></canvas>
  <div id="overlay"><div id="status">Loading virtual feed…</div></div>
  <script>
    (() => {
      const defaultSource = %s;
      const defaultFormat = %s;
      const defaultFit = %q;
      const videoEl = document.getElementById('video');
      const canvasEl = document.getElementById('canvas');
      const overlay = document.getElementById('overlay');
      const statusEl = document.getElementById('status');
      videoEl.style.objectFit = defaultFit;
      canvasEl.style.objectFit = defaultFit;

      const params = new URLSearchParams(window.location.search);
      const fitParam = params.get('fit');
      if (fitParam) {
        videoEl.style.objectFit = fitParam;
        canvasEl.style.objectFit = fitParam;
      }
      const sourceOverride = params.get('source');

      const state = {
        hls: null,
        jsmpeg: null,
        ws: null,
        decoder: null,
        decoderInit: false,
      };
      const jsmpegSources = [
        'https://cdn.jsdelivr.net/gh/phoboslab/jsmpeg@master/jsmpeg.min.js',
        'https://cdn.jsdelivr.net/npm/jsmpeg@1.0.0/jsmpg.js',
        'https://unpkg.com/jsmpeg@1.0.0/jsmpg.js',
      ];

      function loadScript(src) {
        return new Promise((resolve, reject) => {
          const el = document.createElement('script');
          el.src = src;
          el.async = true;
          el.crossOrigin = 'anonymous';
          el.onload = () => resolve();
          el.onerror = () => reject(new Error('failed to load ' + src));
          document.head.appendChild(el);
        });
      }

      async function ensureJsmpeg() {
        if (window.JSMpeg) return;
        let lastErr = null;
        for (const src of jsmpegSources) {
          try {
            await loadScript(src);
            if (window.JSMpeg) return;
          } catch (err) {
            lastErr = err;
          }
        }
        throw lastErr || new Error('jsmpeg failed to load');
      }

      function toWebSocketURL(raw) {
        try {
          const url = new URL(raw, window.location.href);
          if (url.protocol === 'http:') url.protocol = 'ws:';
          if (url.protocol === 'https:') url.protocol = 'wss:';
          return url.toString();
        } catch (err) {
          console.error('invalid websocket url', raw, err);
          return raw;
        }
      }

      function setStatus(msg, isError = false) {
        statusEl.textContent = msg;
        statusEl.style.color = isError ? '#ffb4b4' : '#d9dee9';
        overlay.classList.remove('hidden');
      }

      function hideStatus() {
        overlay.classList.add('hidden');
      }

      function cleanup() {
        if (state.hls) {
          state.hls.destroy();
          state.hls = null;
        }
        if (state.jsmpeg) {
          state.jsmpeg.destroy();
          state.jsmpeg = null;
        }
        if (state.ws) {
          state.ws.close();
          state.ws = null;
        }
        if (state.decoder) {
          state.decoder.close();
          state.decoder = null;
        }
      }

      function pickSourceFromStatus(status) {
        if (status?.ingest?.video) {
          const protocol = status.ingest.video.protocol;
          let fmt = status.ingest.video.format || '';
          if (!fmt && protocol === 'socket') fmt = 'mpegts';
          if (!fmt && protocol === 'webrtc') fmt = 'ivf';
          if (protocol === 'socket' || protocol === 'webrtc') {
            return { kind: 'ws', url: '/input/devices/virtual/feed/socket', format: fmt };
          }
        }
        if (status?.video?.url) {
          return { kind: 'direct', url: status.video.url };
        }
        return null;
      }

      async function resolveSource() {
        if (sourceOverride) {
          return classifySource(sourceOverride, defaultFormat);
        }
        if (defaultSource) {
          return classifySource(defaultSource, defaultFormat);
        }
        try {
          const resp = await fetch('/input/devices/virtual/status');
          if (!resp.ok) throw new Error('status fetch failed');
          const data = await resp.json();
          const candidate = pickSourceFromStatus(data);
          if (candidate) return candidate;
        } catch (err) {
          console.error('status lookup failed', err);
        }
        return null;
      }

      function classifySource(src, fmt) {
        if (!src) return null;
        if (src.startsWith('ws://') || src.startsWith('wss://') || src.startsWith('/input/devices/virtual/feed/socket')) {
          return { kind: 'ws', url: src, format: fmt || (defaultFormat || 'mpegts') };
        }
        if (src.startsWith('http://') || src.startsWith('https://') || src.startsWith('/')) {
          return { kind: 'direct', url: src };
        }
        return null;
      }

      async function start() {
        const source = await resolveSource();
        if (!source) {
          setStatus('No virtual video feed is configured yet.', true);
          return;
        }
        if (source.kind === 'direct') {
          startDirect(source.url);
        } else if (source.kind === 'ws') {
          await startWebsocket(source.url, source.format || 'mpegts');
        } else {
          setStatus('Unsupported source type.', true);
        }
      }

      function startDirect(url) {
        cleanup();
        canvasEl.classList.add('hidden');
        videoEl.classList.remove('hidden');
        const useHls = url.endsWith('.m3u8');
        if (useHls && window.Hls?.isSupported()) {
          state.hls = new Hls({ autoStartLoad: true, liveDurationInfinity: true });
          state.hls.loadSource(url);
          state.hls.attachMedia(videoEl);
          state.hls.on(Hls.Events.MANIFEST_PARSED, () => {
            hideStatus();
            videoEl.play().catch(() => {});
          });
          state.hls.on(Hls.Events.ERROR, (evt, data) => {
            console.error('hls error', data);
            setStatus('Unable to play HLS feed.');
          });
        } else {
          videoEl.src = url;
          videoEl.onloadeddata = hideStatus;
          videoEl.onerror = () => setStatus('Unable to load video source.', true);
          videoEl.play().catch(() => {});
        }
      }

      async function startWebsocket(url, format) {
        cleanup();
        if (format === 'ivf') {
          startIvfWebsocket(url);
          return;
        }
        const wsURL = toWebSocketURL(url);
        try {
          await ensureJsmpeg();
        } catch (err) {
          console.error('jsmpeg failed to load', err);
          setStatus('Unable to load MPEG-TS player.', true);
          return;
        }
        canvasEl.classList.remove('hidden');
        videoEl.classList.add('hidden');
        hideStatus();
        state.jsmpeg = new JSMpeg.Player(wsURL, {
          canvas: canvasEl,
          autoplay: true,
          audio: false,
          loop: true,
        });
      }

      function startIvfWebsocket(url) {
        if (!('VideoDecoder' in window)) {
          setStatus('WebCodecs is required for this WebRTC feed preview.', true);
          return;
        }
        canvasEl.classList.remove('hidden');
        videoEl.classList.add('hidden');
        hideStatus();

        const socket = new WebSocket(toWebSocketURL(url));
        socket.binaryType = 'arraybuffer';
        state.ws = socket;

        let buffer = new Uint8Array();
        let header = null;
        let decoder = null;
        let frameId = 0;

        const isIvfHeader = (bytes) =>
          bytes.length >= 4 &&
          bytes[0] === 0x44 &&
          bytes[1] === 0x4b &&
          bytes[2] === 0x49 &&
          bytes[3] === 0x46;

        function resetDecoderState() {
          if (decoder) {
            decoder.close();
            decoder = null;
          }
          header = null;
          frameId = 0;
        }

        function ensureDecoder(codec, width, height) {
          if (decoder) return decoder;
          decoder = new VideoDecoder({
            output: async (frame) => {
              try {
                canvasEl.width = frame.codedWidth;
                canvasEl.height = frame.codedHeight;
                const bitmap = await createImageBitmap(frame);
                const ctx = canvasEl.getContext('2d');
                ctx.drawImage(bitmap, 0, 0, canvasEl.width, canvasEl.height);
                bitmap.close();
              } finally {
                frame.close();
              }
            },
            error: (err) => console.error('decoder error', err),
          });
          decoder.configure({ codec, codedWidth: width, codedHeight: height });
          state.decoder = decoder;
          return decoder;
        }

        function process() {
          while (buffer.length > 0) {
            if (isIvfHeader(buffer)) {
              resetDecoderState();
            }
            if (!header) {
              if (buffer.length < 32) return;
              header = buffer.slice(0, 32);
              buffer = buffer.slice(32);
              const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
              const codecTag = String.fromCharCode(header[8], header[9], header[10], header[11]);
              const width = view.getUint16(12, true);
              const height = view.getUint16(14, true);
              const codec = codecTag === 'VP90' ? 'vp9' : 'vp8';
              ensureDecoder(codec, width, height);
            }
            if (buffer.length < 12) return;
            const frameHeader = buffer.slice(0, 12);
            const size = new DataView(frameHeader.buffer, frameHeader.byteOffset, frameHeader.byteLength).getUint32(0, true);
            if (buffer.length < 12 + size) return;
            const frameData = buffer.slice(12, 12 + size);
            buffer = buffer.slice(12 + size);
            if (decoder && decoder.state === 'configured') {
              const chunk = new EncodedVideoChunk({
                timestamp: frameId++,
                type: frameId === 1 ? 'key' : 'delta',
                data: frameData,
              });
              decoder.decode(chunk);
            }
          }
        }

        socket.onmessage = (evt) => {
          if (typeof evt.data === 'string') return;
          const next = new Uint8Array(evt.data);
          const merged = new Uint8Array(buffer.length + next.length);
          merged.set(buffer, 0);
          merged.set(next, buffer.length);
          buffer = merged;
          process();
        };
        socket.onclose = () => {
          setStatus('Feed disconnected. Reconnecting…');
          setTimeout(() => startWebsocket(url, 'ivf'), 1200);
        };
        socket.onerror = () => setStatus('Feed websocket error', true);
      }

      start().catch((err) => {
        console.error('failed to start virtual feed', err);
        setStatus('Unable to load virtual feed.', true);
      });
    })();
  </script>
</body>
</html>
`, fit, sourceJSON, formatJSON, fit)
}
