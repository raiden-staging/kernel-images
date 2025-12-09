#!/usr/bin/env node
import fs from 'node:fs';
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
const FEED_URL = process.env.FEED_SOCKET_URL || `ws://${HOST}/input/devices/virtual/feed/socket`;
const DEFAULT_BASE = 'feed_capture';
const DEFAULT_FORMAT = 'mpegts';
const CUSTOM_PATH = process.env.FEED_CAPTURE_FILE ? resolve(__dirname, process.env.FEED_CAPTURE_FILE) : null;

let formatHint = null;
let outPath = CUSTOM_PATH;
let outStream = null;
let captureClosed = false;
let closeResolver;
const closed = new Promise((resolve) => {
  closeResolver = resolve;
});

function resolveClosed() {
  if (captureClosed) {
    return;
  }
  captureClosed = true;
  closeResolver();
}

function formatExtension(format) {
  if (!format) {
    return '.bin';
  }
  switch (format.toLowerCase()) {
    case 'mpegts':
      return '.mpegts';
    case 'ivf':
      return '.ivf';
    default:
      return format.startsWith('.') ? format : `.${format}`;
  }
}

function determinePath() {
  if (CUSTOM_PATH) {
    return CUSTOM_PATH;
  }
  const ext = formatExtension(formatHint || DEFAULT_FORMAT);
  return resolve(__dirname, `${DEFAULT_BASE}${ext}`);
}

function ensureStream() {
  if (outStream) {
    return outStream;
  }
  outPath = determinePath();
  console.log(`feed capture writing to ${outPath} (format ${formatHint || DEFAULT_FORMAT})`);
  outStream = fs.createWriteStream(outPath);
  outStream.once('close', resolveClosed);
  return outStream;
}

const ws = new WebSocket(FEED_URL);
ws.binaryType = 'arraybuffer';

ws.on('open', () => {
  console.log(`feed connected -> ${FEED_URL}`);
});
ws.on('message', (msg, isBinary) => {
  // Format hints arrive as text frames, but some websocket clients surface them
  // as Uint8Array/Buffer even when `isBinary` is false. Treat any non-binary
  // payload as a hint so it doesn't contaminate the capture file.
  if (isBinary === false) {
    const fmt = Buffer.from(msg).toString('utf8').trim();
    if (fmt) {
      formatHint = fmt;
      console.log(`format hint: ${formatHint}`);
    }
    return;
  }
  if (typeof msg === 'string') {
    const fmt = msg.trim();
    if (fmt) {
      formatHint = fmt;
      console.log(`format hint: ${formatHint}`);
    }
    return;
  }
  const stream = ensureStream();
  stream.write(new Uint8Array(msg));
});
ws.on('close', () => {
  console.log('feed websocket closed');
  if (outStream) {
    outStream.end();
  } else {
    resolveClosed();
  }
});
ws.on('error', (err) => console.error('feed socket error', err));

await closed;
console.log('capture saved');
