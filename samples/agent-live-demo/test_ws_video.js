import 'dotenv/config';
import path from 'node:path';
import { spawn } from 'node:child_process';
import WebSocket from 'ws';

const KERNEL_API_BASE = process.env.KERNEL_API_BASE || 'http://localhost:444';
const VIDEO_PERSONAS = {
  good: path.resolve('avatars/rose.good.ts'),
  evil: path.resolve('avatars/rose.evil.ts'),
};

const log = {
  info: (...args) => console.log(`[INFO] ${new Date().toISOString()}`, ...args),
  ok: (...args) => console.log(`[OK] ${new Date().toISOString()}`, ...args),
  warn: (...args) => console.warn(`[WARN] ${new Date().toISOString()}`, ...args),
  error: (...args) => console.error(`[ERR] ${new Date().toISOString()}`, ...args),
  debug: (...args) => console.log(`[DEBUG] ${new Date().toISOString()}`, ...args),
};

async function httpJson(url, opts = {}) {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json', ...(opts.headers || {}) },
    ...opts,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status} ${res.statusText} for ${url}: ${text}`);
  }
  return res.json();
}

class VideoTester {
  constructor() {
    this.ws = null;
    this.ffmpeg = null;
    this.totalBytesSent = 0;
  }

  async configure() {
    const body = {
      video: { type: 'socket', format: 'mpegts', width: 1080, height: 1350, frame_rate: 30 },
      audio: { type: 'socket', format: 'mp3', destination: 'microphone' }, // Keep audio to avoid breaking config
    };
    log.info('Configuring virtual inputs...');
    const config = await httpJson(`${KERNEL_API_BASE}/input/devices/virtual/configure`, { method: 'POST', body: JSON.stringify(body) });
    return config.ingest.video.url;
  }

  async startStream(ingestUrl, videoPath) {
    const wsUrl = ingestUrl.startsWith('ws') ? ingestUrl : `${KERNEL_API_BASE.replace('http', 'ws')}${ingestUrl}`;
    log.info(`Connecting to Video WS: ${wsUrl}`);

    return new Promise((resolve, reject) => {
        this.ws = new WebSocket(wsUrl);
        
        this.ws.on('open', () => {
            log.ok('Video socket connected');
            this.spawnFfmpeg(videoPath);
            resolve();
        });

        this.ws.on('error', (err) => {
            log.error('WebSocket error:', err.message);
            // Don't reject here to allow observing error flow
        });

        this.ws.on('close', (code, reason) => {
            log.warn(`WebSocket closed: ${code} ${reason}`);
            this.killFfmpeg();
        });
    });
  }

  spawnFfmpeg(videoPath) {
    const args = [
        '-re',
        '-stream_loop', '-1',
        '-i', videoPath,
        '-c', 'copy',
        '-f', 'mpegts',
        'pipe:1'
    ];
    log.debug(`Spawning ffmpeg: ${args.join(' ')}`);
    
    this.ffmpeg = spawn('ffmpeg', args, { stdio: ['ignore', 'pipe', 'pipe'] });
    
    this.ffmpeg.stdout.on('data', chunk => {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(chunk);
            this.totalBytesSent += chunk.length;
            // log.debug(`Sent ${chunk.length} bytes (Total: ${this.totalBytesSent})`);
        }
    });

    this.ffmpeg.stderr.on('data', data => {
        // log.debug(`[FFMPEG] ${data.toString().trim()}`);
    });

    this.ffmpeg.on('exit', (code, signal) => {
        log.warn(`FFmpeg exited code=${code} signal=${signal}`);
    });
    
    this.ffmpeg.on('error', (err) => {
        log.error('FFmpeg error:', err);
    });
  }

  killFfmpeg() {
      if (this.ffmpeg) {
          log.info('Killing FFmpeg...');
          this.ffmpeg.kill('SIGINT');
          this.ffmpeg = null;
      }
  }

  stop() {
      this.killFfmpeg();
      if (this.ws) {
          log.info('Closing WebSocket...');
          this.ws.close();
          this.ws = null;
      }
  }
}

async function run() {
  const tester = new VideoTester();
  
  try {
    const ingestUrl = await tester.configure();
    log.info(`Got ingest URL: ${ingestUrl}`);

    log.info('--- Phase 1: Streaming GOOD persona (10s) ---');
    await tester.startStream(ingestUrl, VIDEO_PERSONAS.good);
    await new Promise(r => setTimeout(r, 10000));

    log.info('--- Phase 2: Switching to EVIL persona ---');
    tester.stop();
    log.info('Waiting 2s before reconnecting...');
    await new Promise(r => setTimeout(r, 2000));
    
    log.info('Reconnecting...');
    await tester.startStream(ingestUrl, VIDEO_PERSONAS.evil);
    await new Promise(r => setTimeout(r, 10000));

    log.info('--- Test Complete ---');
    tester.stop();

  } catch (err) {
    log.error('Test failed:', err);
    tester.stop();
    process.exit(1);
  }
}

run();
