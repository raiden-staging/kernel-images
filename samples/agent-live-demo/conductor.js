#!/usr/bin/env node
// Orchestrates operations.

import 'dotenv/config';
import path from 'node:path';
import { spawn } from 'node:child_process';
import express from 'express';
import WebSocket from 'ws';
import fs from 'node:fs';
import { ElevenLabsClient } from '@elevenlabs/elevenlabs-js';

const KERNEL_API_BASE = process.env.KERNEL_API_BASE || 'http://localhost:444';
const REMOTE_RTMP_URL = process.env.REMOTE_RTMP_URL || process.env.REMOTE_RTMP_TARGET || '';
const ENABLE_REMOTE_LIVESTREAM = false;
const ENABLE_VIDEO_STREAMER = true;
const ELEVENLABS_API_KEY = process.env.ELEVENLABS_API_KEY || '';
const ELEVENLABS_AGENT_ID = process.env.ELEVENLABS_AGENT_ID || '';
const MOONDREAM_API_KEY = process.env.MOONDREAM_API_KEY || '';
const MAX_CONTEXT_MESSAGES = 4;

const AUDIO_SAMPLE_RATE = 16_000;
const PCM_CHUNK_SECONDS = 0.25;
const PCM_CHUNK_BYTES = AUDIO_SAMPLE_RATE * PCM_CHUNK_SECONDS * 2;

const PORT = 3117;
const VIDEO_PERSONAS = {
  good: path.resolve('avatars/rose.good.ts'),
  evil: path.resolve('avatars/rose.evil.ts'),
};

const argvMeeting = (() => {
  const idx = process.argv.findIndex((arg) => arg === '--meeting_url');
  if (idx !== -1 && process.argv[idx + 1]) return process.argv[idx + 1];
  return '';
})();

const log = {
  info: (...args) => console.log(`[INFO] ${new Date().toISOString()}`, ...args),
  ok: (...args) => console.log(`[OK] ${new Date().toISOString()}`, ...args),
  warn: (...args) => console.warn(`[WARN] ${new Date().toISOString()}`, ...args),
  error: (...args) => console.error(`[ERR] ${new Date().toISOString()}`, ...args),
  debug: (...args) => console.log(`[DEBUG] ${new Date().toISOString()}`, ...args),
};

function assertEnv() {
  if (!REMOTE_RTMP_URL) throw new Error('REMOTE_RTMP_URL is required');
  if (!ELEVENLABS_API_KEY) throw new Error('ELEVENLABS_API_KEY is required');
}

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

class KernelApi {
  constructor(base = KERNEL_API_BASE) {
    this.base = base.replace(/\/$/, '');
    this.virtualConfig = null;
  }

  async listStreams() {
    const url = `${this.base}/stream/list`;
    return httpJson(url);
  }

  async configureVirtualInputs() {
    const body = {
      video: { type: 'socket', format: 'mpegts', width: 1080, height: 1350, frame_rate: 30 },
      audio: { type: 'socket', format: 'mp3', destination: 'microphone' },
    };
    const url = `${this.base}/input/devices/virtual/configure`;
    this.virtualConfig = await httpJson(url, { method: 'POST', body: JSON.stringify(body) });
    return this.virtualConfig;
  }

  async startRemoteLivestream(targetUrl = REMOTE_RTMP_URL, id = 'remote-rtmp') {
    const body = { id, mode: 'remote', target_url: targetUrl };
    const url = `${this.base}/stream/start`;
    try {
      const res = await httpJson(url, { method: 'POST', body: JSON.stringify(body) });
      return res;
    } catch (err) {
      if ((err?.message || '').includes('409')) {
        // Reuse existing stream if already running
        try {
          const streams = await this.listStreams();
          const existing = Array.isArray(streams)
            ? streams.find((s) => s.id === id || s.target_url === targetUrl)
            : null;
          if (existing) {
            log.warn('Remote livestream already running; reusing existing stream', existing);
            return existing;
          }
        } catch {
          // fall through to throw original error
        }
      }
      throw err;
    }
  }

  async startAudioLivestreamSocket(id = 'audio-live') {
    const body = { id, mode: 'socket' };
    const url = `${this.base}/stream/start`;
    try {
      return await httpJson(url, { method: 'POST', body: JSON.stringify(body) });
    } catch (err) {
      if ((err?.message || '').includes('409')) {
        try {
          const streams = await this.listStreams();
          const existing = Array.isArray(streams) ? streams.find((s) => s.id === id) : null;
          if (existing) {
            log.warn('Audio livestream already running; reusing existing stream', existing);
            return existing;
          }
        } catch {
          // ignore
        }
      }
      throw err;
    }
  }

  async feedSocketInfo() {
    const url = `${this.base}/input/devices/virtual/feed/socket/info`;
    return httpJson(url);
  }

  async devtoolsWebsocket() {
    const candidates = ['http://localhost:9222/json/version', 'http://127.0.0.1:9222/json/version'];
    let lastErr;
    for (const url of candidates) {
      try {
        const res = await fetch(url, { headers: { Host: 'localhost' } });
        if (!res.ok) {
          lastErr = new Error(`Devtools fetch ${url} returned ${res.status}`);
          continue;
        }
        const data = await res.json();
        const ws =
          data.webSocketDebuggerUrl ||
          data.websocketDebuggerUrl ||
          data.webSocketDebuggerURL ||
          (Array.isArray(data) ? data[0]?.webSocketDebuggerUrl : null);
        if (ws) return ws;
        lastErr = new Error(`Devtools response missing webSocketDebuggerUrl at ${url}`);
      } catch (err) {
        lastErr = err;
      }
    }
    throw lastErr || new Error('Unable to resolve devtools websocket endpoint');
  }

  async stopAllStreams() {
    const url = `${this.base}/stream/stop`;
    try {
      await fetch(url, { method: 'POST' });
    } catch (err) {
      log.warn('Failed to stop streams (ignored)', err?.message || err);
    }
  }

  async takeScreenshot() {
    const url = `${this.base}/computer/screenshot`;
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({})
    });
    if (!res.ok) throw new Error(`Screenshot failed: ${res.status}`);
    return res.arrayBuffer();
  }
}

class AudioPipelines {
  constructor() {
    this.decoder = null;
    this.encoder = null;
    this.encoderSocket = null;
    this.decoderInput = null;
    this.incomingBuffer = Buffer.alloc(0);
    this.totalBytesSent = 0;
  }

  startMeetingAudioIngest(socketUrl, onPcmChunk) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(socketUrl);
      ws.binaryType = 'arraybuffer';
      ws.on('open', () => log.ok(`meeting audio websocket connected: ${socketUrl}`));
      ws.on('message', (data) => {
        if (!this.decoder) return;
        this.decoder.stdin.write(Buffer.from(data));
      });
      ws.on('error', reject);
      ws.on('close', () => log.warn('meeting audio websocket closed'));

      const isMpegTs = socketUrl.includes('/stream/socket/');
      const args = isMpegTs
        ? [
            '-fflags',
            'nobuffer',
            '-analyzeduration',
            '0',
            '-probesize',
            '32k',
            '-i',
            'pipe:0',
            '-vn',
            '-ac',
            '1',
            '-ar',
            String(AUDIO_SAMPLE_RATE),
            '-f',
            's16le',
            'pipe:1',
          ]
        : [
            '-f',
            'mp3',
            '-i',
            'pipe:0',
            '-ac',
            '1',
            '-ar',
            String(AUDIO_SAMPLE_RATE),
            '-f',
            's16le',
            'pipe:1',
          ];
      this.decoder = spawn('ffmpeg', args, { stdio: ['pipe', 'pipe', 'inherit'] });
      this.decoder.stdout.on('data', (chunk) => {
        this.incomingBuffer = Buffer.concat([this.incomingBuffer, chunk]);
        while (this.incomingBuffer.length >= PCM_CHUNK_BYTES) {
          const pcm = this.incomingBuffer.subarray(0, PCM_CHUNK_BYTES);
          this.incomingBuffer = this.incomingBuffer.subarray(PCM_CHUNK_BYTES);
          onPcmChunk(pcm);
        }
      });
      this.decoder.on('error', reject);
      this.decoder.on('spawn', resolve);
    });
  }

  startMicOutputEncoder(ingestUrl) {
    return new Promise((resolve, reject) => {
      const resolvedUrl = ingestUrl.startsWith('ws')
        ? ingestUrl
        : `${KERNEL_API_BASE.replace('http', 'ws')}${ingestUrl}`;
      this.encoderSocket = new WebSocket(resolvedUrl);
      this.encoderSocket.on('open', () => log.ok(`virtual mic socket connected: ${ingestUrl}`));
      this.encoderSocket.on('close', () => log.warn('virtual mic socket closed'));
      this.encoderSocket.on('error', (err) => log.error('virtual mic socket error', err));

      const args = [
        '-f',
        's16le',
        '-ar',
        String(AUDIO_SAMPLE_RATE),
        '-ac',
        '1',
        '-i',
        'pipe:0',
        '-codec:a',
        'libmp3lame',
        '-b:a',
        '128k',
        '-ar',
        '44100',
        '-ac',
        '1',
        '-f',
        'mp3',
        '-fflags',
        '+nobuffer',
        'pipe:1',
      ];
      log.debug('Spawning ffmpeg encoder with args:', args.join(' '));
      this.encoder = spawn('ffmpeg', args, { stdio: ['pipe', 'pipe', 'pipe'] }); // Changed to 'pipe' for stderr
      
      this.encoder.stderr.on('data', (data) => {
          log.debug(`[FFMPEG-STDERR] ${data.toString()}`);
      });

      this.encoder.stdout.on('data', (chunk) => {
        if (this.encoderSocket && this.encoderSocket.readyState === WebSocket.OPEN) {
          this.encoderSocket.send(chunk);
          this.totalBytesSent += chunk.length;
          log.debug(`Sent ${chunk.length} bytes to audio socket (Total: ${this.totalBytesSent})`);
        } else {
            log.warn('Encoder produced data but socket not open');
        }
      });
      this.encoder.on('error', reject);
      this.encoder.on('spawn', resolve);
    });
  }

    writeAgentPcm(pcmBuffer) {
      if (!this.encoder || !this.encoder.stdin.writable) {
          log.warn('writeAgentPcm: encoder not writable');
          return;
      }
      log.debug(`Writing ${pcmBuffer.length} bytes PCM to encoder`);
      this.encoder.stdin.write(pcmBuffer);
    }
  stop() {
    if (this.decoder) this.decoder.kill('SIGINT');
    if (this.encoder) this.encoder.kill('SIGINT');
    if (this.encoderSocket) this.encoderSocket.close();
    this.decoder = null;
    this.encoder = null;
    this.encoderSocket = null;
    this.incomingBuffer = Buffer.alloc(0);
  }
}

class ElevenLabsBridge {
  constructor(audioPipelines, kernelApi, onPersonaSwitch) {
    this.audioPipelines = audioPipelines;
    this.kernelApi = kernelApi;
    this.onPersonaSwitch = onPersonaSwitch;
    this.ws = null;
    this.client = new ElevenLabsClient({ apiKey: ELEVENLABS_API_KEY });
    this.currentPersona = 'good';
    this.totalBytesReceived = 0;
    this.contextMessages = [];
  }

  async verifyAgent() {
    await this.client.conversationalAi.agents.get(ELEVENLABS_AGENT_ID);
    log.ok('ElevenLabs agent verified');
  }

  async connect() {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) return;
    const url = `wss://api.elevenlabs.io/v1/convai/conversation?agent_id=${encodeURIComponent(ELEVENLABS_AGENT_ID)}`;
    this.ws = new WebSocket(url);

    this.ws.on('open', () => {
      log.ok('ElevenLabs websocket connected');
      this._sendInit();
    });
    this.ws.on('message', (data) => this._handleMessage(data));
    this.ws.on('close', () => log.warn('ElevenLabs websocket closed'));
    this.ws.on('error', (err) => log.error('ElevenLabs websocket error', err));
    await new Promise((resolve) => this.ws.once('open', resolve));
  }

  sendUserAudio(pcmChunk) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    const payload = { user_audio_chunk: pcmChunk.toString('base64') };
    this.ws.send(JSON.stringify(payload));
  }

  stop() {
    if (this.ws) this.ws.close();
    this.ws = null;
  }

  _sendInit() {
    const systemPrompt = [
      `You are ${ELEVENLABS_AGENT_ID} and named Rose.`,
      'You have two personas: "good" (default) and "evil".',
      'If user asks to switch persona, respond with "good mode on" or "evil mode on". good is nice, evil is overly sarcastic.',
      'Be quirky and concise.',
	  '',
      'You have visual capabilities. You can see. If user asks about ANYTHING related to visual, screen, ("what do you see ..." / "how does X look" / "look at this" / any variation) , call get_visual tool directly (without asking the user for confirmation, do this everytime it happens) before you answer them.',
      'The user does not need to provide anything. If the user asks for anything visual capability related, call the get_visual tool IMMEDIATELY ALWAYS !',
    ].join(' ');

    const payload = {
      type: 'conversation_initiation_client_data',
      conversation_config_override: {
        agent: {
          prompt: { prompt: systemPrompt },
          first_message: 'hey there - Rose here.',
          language: 'en',
          client_tools: [
            {
              name: 'get_visual',
              description: 'Gets the current visual.',
              parameters: {
                type: 'object',
                properties: {},
                required: []
              },
              expects_response: true
            }
          ]
        },
      },
    };
    this.ws.send(JSON.stringify(payload));
  }

  _handleMessage(raw) {
    let msg;
    try {
      msg = JSON.parse(raw.toString());
    } catch (err) {
      log.error('Failed to parse ElevenLabs message', err);
      return;
    }
    const type = msg.type;
    log.debug(`ElevenLabs Msg: ${type}`);
    
    if (type === 'audio') {
      const base64 = msg.audio_event?.audio_base_64;
      if (!base64) return;
      const pcm = Buffer.from(base64, 'base64');
      this.totalBytesReceived += pcm.length;
      log.debug(`Rx Audio: ${pcm.length} bytes (Total: ${this.totalBytesReceived})`);
      this.audioPipelines.writeAgentPcm(pcm);
      return;
    }
    if (type === 'user_transcription') {
      const text = msg.user_transcription_event?.user_transcription || '';
      if (text) {
        this.contextMessages.push(`User: ${text}`);
        if (this.contextMessages.length > MAX_CONTEXT_MESSAGES) this.contextMessages.shift();
      }
      return;
    }
    if (type === 'agent_response') {
      const text = msg.agent_response_event?.agent_response || '';
      if (text) {
        this.contextMessages.push(`Agent: ${text}`);
        if (this.contextMessages.length > MAX_CONTEXT_MESSAGES) this.contextMessages.shift();
      }
      log.info(`Agent text: ${text}`);
      this._maybeSwitchPersona(text);
      return;
    }
    if (type === 'ping') {
      const eventId = msg.ping_event?.event_id;
      this.ws?.send(JSON.stringify({ type: 'pong', event_id: eventId }));
      return;
    }
    if (type === 'client_tool_call') {
      this._handleClientToolCall(msg.client_tool_call);
      return;
    }
  }

  async _handleClientToolCall(toolCall) {
    const { tool_name, tool_call_id } = toolCall;
    if (tool_name === 'get_visual') {
      log.info(`[Tool:get_visual] Analysis requested`);
      try {
        const pngBuffer = await this.kernelApi.takeScreenshot();
        const base64Image = `data:image/png;base64,${Buffer.from(pngBuffer).toString('base64')}`;
        
        const contextText = this.contextMessages.join('\n');
        const visionPrompt = `the image is a screenshot taken from an ongoing meeting. Here is the recent conversation context:\n${contextText}\n\nDescribe in vivid detail all you can based on the context.`;
        
        const res = await fetch('https://api.moondream.ai/v1/query', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-Moondream-Auth': MOONDREAM_API_KEY
            },
            body: JSON.stringify({
                image_url: base64Image,
                question: visionPrompt,
                reasoning: true
            })
        });

        if (!res.ok) {
            const txt = await res.text();
            throw new Error(`Moondream API error: ${res.status} ${txt}`);
        }

        const data = await res.json();
        const result = data.answer;
        
        log.info(`[Tool:get_visual] Result: ${result}`);
        
        const responsePayload = {
            type: 'client_tool_result',
            client_tool_result: {
                tool_call_id,
                result
            }
        };
        this.ws.send(JSON.stringify(responsePayload));
      } catch (err) {
        log.error('[Tool:get_visual] Error:', err);
        const responsePayload = {
            type: 'client_tool_result',
            client_tool_result: {
                tool_call_id,
                result: `Error processing visual: ${err.message}`
            }
        };
        this.ws.send(JSON.stringify(responsePayload));
      }
    }
  }

  _maybeSwitchPersona(text) {
    const lower = (text || '').toLowerCase();
    if (lower.includes('evil') && this.currentPersona !== 'evil') {
      this.currentPersona = 'evil';
      this.onPersonaSwitch('evil');
    } else if (lower.includes('good') && this.currentPersona !== 'good') {
      this.currentPersona = 'good';
      this.onPersonaSwitch('good');
    }
  }
}

class SocketVideoStreamer {
  constructor() {
    this.ffmpeg = null;
    this.ws = null;
    this.currentPersona = 'good';
    this.ingestUrl = null;
    this.totalBytesSent = 0;
  }

  async start(ingestUrl, persona) {
    this.ingestUrl = ingestUrl;
    if (!this.ingestUrl.startsWith('ws')) {
       this.ingestUrl = `${KERNEL_API_BASE.replace('http', 'ws')}${this.ingestUrl}`;
    }
    await this.switchPersona(persona || this.currentPersona);
  }

  async switchPersona(persona) {
    if (persona !== 'good' && persona !== 'evil') return;
    this.currentPersona = persona;
    await this._restart();
  }

  async _restart() {
    this.stop();
    if (!this.ingestUrl) return;

    log.info(`Stopping previous stream... waiting 1s for cleanup...`);
    await new Promise(r => setTimeout(r, 1000));

    log.info(`Starting Video Streamer (${this.currentPersona}) -> ${this.ingestUrl}`);
    
    this.ws = new WebSocket(this.ingestUrl);
    this.ws.on('open', () => {
        log.ok(`Video socket connected`);
        this._spawnFfmpeg();
    });
    this.ws.on('error', (err) => log.error('Video socket error', err));
    this.ws.on('close', () => {
        log.warn('Video socket closed');
        this._killFfmpeg();
    });
  }

  _spawnFfmpeg() {
    const videoPath = VIDEO_PERSONAS[this.currentPersona];
    const args = [
        '-re',
        '-stream_loop', '-1',
        '-i', videoPath,
        '-c', 'copy',
        '-f', 'mpegts',
        'pipe:1'
    ];
    
    log.debug(`Spawning video ffmpeg: ${args.join(' ')}`);
    this.ffmpeg = spawn('ffmpeg', args, { stdio: ['ignore', 'pipe', 'pipe'] });
    
    this.ffmpeg.stdout.on('data', chunk => {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(chunk);
            this.totalBytesSent += chunk.length;
        }
    });
    
    this.ffmpeg.stderr.on('data', data => {
        // Optional: log.debug(`[VIDEO-FFMPEG] ${data.toString()}`);
    });
    
    this.ffmpeg.on('exit', (code, signal) => {
        log.warn(`Video ffmpeg exited code=${code} signal=${signal}`);
    });
  }

  _killFfmpeg() {
    if (this.ffmpeg) {
        this.ffmpeg.kill('SIGINT');
        this.ffmpeg = null;
    }
  }

  stop() {
    this._killFfmpeg();
    if (this.ws) {
        this.ws.close();
        this.ws = null;
    }
  }
}

class BrowserFlow {
  constructor(kernelApi) {
    this.kernelApi = kernelApi;
    this.agent = null;
    this.feedUrl = null;
    this.meetingUrl = null;
    this.feedTitle = 'Virtual Feed';
  }

  async run(meetingUrl) {
    this.meetingUrl = meetingUrl;
    this.feedUrl = `${KERNEL_API_BASE}/input/devices/virtual/feed?fit=cover`;
    const feedPage = await this.agent.newPage(); // new tab
    await feedPage.goto(this.feedUrl);
    await feedPage.evaluate((title) => {
      document.title = title;
    }, this.feedTitle);

    const meetingPage = await this.agent.newPage(); // new tab for meeting
    const origin = new URL(meetingUrl).origin;
    await meetingPage.context().grantPermissions(['microphone', 'camera', 'display-capture'], { origin });
    await meetingPage.goto(meetingUrl, { waitUntil: 'domcontentloaded' });

    const instructions = [
      'Join the meeting in this tab. Steps:',
      '1) Accept/allow all camera/microphone/screen-share prompts.',
      '2) Click Join/Ask to join if needed.',
      `3) Start presenting a tab and select the tab titled "${this.feedTitle}" (URL: ${this.feedUrl}).`,
      '4) Keep mic on and stay connected. Confirm share if any extra popups appear.',
      'Be decisive; retry clicks if blocked by dialogs.',
    ].join(' ');
    await meetingPage.ai(instructions);
    log.ok('Browser agent AI instructed to join meeting and share virtual feed tab');
  }
}

class Conductor {
  constructor(meetingUrl) {
    this.meetingUrl = meetingUrl || argvMeeting || '';
    this.kernel = new KernelApi();
    this.audio = new AudioPipelines();
    this.video = new SocketVideoStreamer();
    this.bridge = new ElevenLabsBridge(this.audio, this.kernel, (persona) => this.video.switchPersona(persona));
    this.browserFlow = new BrowserFlow(this.kernel);
    this.virtualConfig = null;
    this.audioLivestreamInfo = null;
    this.server = null;
    this.prepared = false;
    this.audioSocketUrl = null;
  }

  async prepare() {
    if (this.prepared) {
      log.warn('Prepare already completed; skipping');
      return;
    }
    assertEnv();
    log.info('Configuring virtual inputs (webrtc video + socket mic)...');
    this.virtualConfig = await this.kernel.configureVirtualInputs();
    const audioIngestUrl = this.virtualConfig?.ingest?.audio?.url;
    const videoOfferUrl = this.virtualConfig?.ingest?.video?.url;
    if (!audioIngestUrl || !videoOfferUrl) {
      throw new Error('Virtual input ingest URLs missing');
    }

    await new Promise((r) => setTimeout(r, 3000));

    log.info('Starting audio livestream (socket)...');
    this.audioLivestreamInfo = await this.kernel.startAudioLivestreamSocket('meeting-audio');
    this.audioSocketUrl = this.audioLivestreamInfo.websocket_url || this.audioLivestreamInfo.web_socket_url;
    if (!this.audioSocketUrl) throw new Error('Audio livestream websocket missing');

    await new Promise((r) => setTimeout(r, 3000));

    if (ENABLE_REMOTE_LIVESTREAM) {
      log.info('Starting remote RTMP livestream...');
      await this.kernel.startRemoteLivestream();
    } else {
      log.info('Remote RTMP livestream disabled (ENABLE_REMOTE_LIVESTREAM=false)');
    }

    await this.bridge.verifyAgent();
    log.ok('Preparation complete (no ElevenLabs session started)');
    this.prepared = true;
  }

  async startSession() {
    if (!this.prepared) throw new Error('Run /prepare first (sets up ingest URLs)');
    const audioIngestUrl = this.virtualConfig?.ingest?.audio?.url;
    const videoOfferUrl = this.virtualConfig?.ingest?.video?.url;
    if (!audioIngestUrl || !videoOfferUrl || !this.audioSocketUrl) {
      throw new Error('Missing ingest URLs; rerun /prepare');
    }

    log.info('Starting meeting audio ingest decoder -> ElevenLabs input...');
    const audioSocketResolved = this.audioSocketUrl.startsWith('ws')
      ? this.audioSocketUrl
      : `${KERNEL_API_BASE.replace('http', 'ws')}${this.audioSocketUrl}`;
    await this.audio.startMeetingAudioIngest(audioSocketResolved, (pcm) => this.bridge.sendUserAudio(pcm));

    log.info('Starting virtual microphone encoder (agent -> kernel)...');
    await this.audio.startMicOutputEncoder(audioIngestUrl);

    if (ENABLE_VIDEO_STREAMER) {
      log.info('Starting Socket video streamer (good persona)...');
      await this.video.start(videoOfferUrl, 'good');
    } else {
      log.info('Socket video streamer disabled (ENABLE_VIDEO_STREAMER=false)');
    }

    await this.bridge.connect();
    log.ok('ElevenLabs session started');
  }

  async stopSession() {
    this.bridge.stop();
    this.audio.stop();
    this.video.stop();
    log.ok('Session stopped');
  }

  async joinMeeting() {
    if (!this.prepared) throw new Error('Run /prepare first so Chrome is restarted after virtual inputs are configured');
    if (!this.meetingUrl) throw new Error('meeting_url was not provided');
    await this.browserFlow.run(this.meetingUrl);
  }

  listen(port = PORT) {
    const app = express();
    app.use(express.json());

    app.get('/health', (_req, res) => res.json({ ok: true }));
    app.post('/prepare', async (_req, res) => {
      try {
        await this.prepare();
        res.json({ status: 'ready' });
      } catch (err) {
        log.error(err);
        res.status(500).json({ error: err.message });
      }
    });
    app.post('/session/start', async (_req, res) => {
      try {
        await this.startSession();
        res.json({ status: 'started' });
      } catch (err) {
        log.error(err);
        res.status(500).json({ error: err.message });
      }
    });
    app.post('/session/stop', async (_req, res) => {
      try {
        await this.stopSession();
        res.json({ status: 'stopped' });
      } catch (err) {
        log.error(err);
        res.status(500).json({ error: err.message });
      }
    });
    app.post('/browser/join', async (_req, res) => {
      try {
        await this.joinMeeting();
        res.json({ status: 'meeting automation triggered' });
      } catch (err) {
        log.error(err);
        res.status(500).json({ error: err.message });
      }
    });

    this.server = app.listen(port, () => {
      log.ok(`Conductor server listening on port ${port}`);
    });
  }
}

async function main() {
  const conductor = new Conductor(argvMeeting);
  conductor.listen();
}

main().catch((err) => {
  log.error(err);
  process.exit(1);
});
