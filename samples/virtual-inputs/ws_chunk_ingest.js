#!/usr/bin/env node
import { createReadStream } from 'node:fs';
import { once } from 'node:events';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

let WebSocket;
try {
  ({ default: WebSocket } = await import('ws'));
} catch (err) {
  console.error('Install the ws dependency (npm install ws) before running this script.');
  process.exit(1);
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const HOST = process.env.VIRTUAL_INPUT_HOST || 'localhost:444';
const VIDEO_FORMAT = 'mpegts';
const AUDIO_FORMAT = (process.env.AUDIO_FORMAT || 'mp3').toLowerCase();
const VIDEO_FILE = process.env.VIDEO_FILE || 'sample_video_mpeg1.ts';
const AUDIO_FILE = process.env.AUDIO_FILE || 'sample_audio.mp3';
const parsedDelay = Number(process.env.CHUNK_DELAY_MS || '35');
const CHUNK_DELAY_MS = Number.isFinite(parsedDelay) ? parsedDelay : 35;
const HIGH_WATER_MARK = 64 * 1024;

function mediaPath(fileName) {
  return resolve(__dirname, 'media', fileName);
}

async function pump({ url, file, label }) {
  const ws = new WebSocket(url);
  ws.binaryType = 'arraybuffer';
  ws.on('message', (msg) => console.log(`${label} format hint:`, msg.toString()));
  ws.on('close', () => console.log(`${label} ingest closed`));
  ws.on('error', (err) => console.error(`${label} socket error`, err));

  await once(ws, 'open');
  console.log(`${label} connected -> ${url} (${file})`);

  let chunks = 0;
  let bytes = 0;
  for await (const chunk of createReadStream(file, { highWaterMark: HIGH_WATER_MARK })) {
    ws.send(chunk);
    bytes += chunk.length;
    chunks += 1;
    await delay(CHUNK_DELAY_MS);
  }

  console.log(`${label} sent ${chunks} chunks (${bytes} bytes). Socket left open for realtime feed or additional chunks.`);
  return ws;
}

const baseUrl = `ws://${HOST}/input/devices/virtual/socket`;
const video = await pump({ url: `${baseUrl}/video`, file: mediaPath(VIDEO_FILE), label: 'video' });
const audio = await pump({ url: `${baseUrl}/audio`, file: mediaPath(AUDIO_FILE), label: 'audio' });

console.log(`Streaming with video=${VIDEO_FILE} (${VIDEO_FORMAT}) and audio=${AUDIO_FILE} (${AUDIO_FORMAT}).`);
console.log('Press Ctrl+C to stop; you can also send more chunks from another script using these sockets.');
await new Promise(() => {});
