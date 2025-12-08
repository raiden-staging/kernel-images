#!/usr/bin/env node
import fs from 'node:fs';
import { once } from 'node:events';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

let WebSocket;
try {
  ({ default: WebSocket } = await import('ws'));
} catch (err) {
  console.error('Install the ws dependency (npm install ws) before running this script.');
  process.exit(1);
}

const HOST = process.env.VIRTUAL_INPUT_HOST || 'localhost:444';
const OUT_FILE = process.env.FEED_CAPTURE_FILE || 'feed_capture.mpegts';
const FEED_URL = process.env.FEED_SOCKET_URL || `ws://${HOST}/input/devices/virtual/feed/socket`;
const outPath = resolve(__dirname, OUT_FILE);

const ws = new WebSocket(FEED_URL);
ws.binaryType = 'arraybuffer';

const out = fs.createWriteStream(outPath);
ws.on('open', () => console.log(`feed connected -> ${FEED_URL} (writing to ${outPath})`));
ws.on('message', (msg) => {
  if (typeof msg === 'string') {
    console.log(`format hint: ${msg}`);
    return;
  }
  out.write(new Uint8Array(msg));
});
ws.on('close', () => {
  console.log('feed websocket closed');
  out.end();
});
ws.on('error', (err) => console.error('feed socket error', err));

await once(out, 'close');
console.log('capture saved');
