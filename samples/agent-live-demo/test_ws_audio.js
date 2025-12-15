import { spawn } from 'node:child_process';
import path from 'node:path';
import WebSocket from 'ws';
import fs from 'node:fs';

const files = process.argv.slice(2);
if (files.length === 0) {
  console.error('Usage: node test_ws_audio.js <file1> [file2] ...');
  process.exit(1);
}

const wsUrl = 'ws://localhost:444/input/devices/virtual/socket/audio';
const ws = new WebSocket(wsUrl);

console.log(`Connecting to: ${wsUrl}`);

ws.on('open', async () => {
  console.log('Connected.');

  for (let i = 0; i < files.length; i++) {
    const filename = files[i];
    const filePath = path.join(filename);
    
    if (!fs.existsSync(filePath)) {
        console.error(`File not found: ${filePath}, skipping.`);
        continue;
    }

    console.log(`\n[${i+1}/${files.length}] Playing: ${filename}`);
    try {
      await streamFile(filePath);
    } catch (err) {
      console.error(`Failed to play ${filename}:`, err);
    }
    
    if (i < files.length - 1) {
        console.log('Waiting 0.5s...');
        await new Promise(r => setTimeout(r, 500));
    }
  }

  console.log('\nAll files sent. Sending 5s padding silence...');
  try {
    await streamSilence(5);
  } catch (err) {
    console.error('Failed to send silence padding:', err);
  }

  console.log('\nDone. Connection remaining open. Press Ctrl+C to exit.');
});

ws.on('error', (err) => {
  console.error('\nWebSocket error:', err.message);
  process.exit(1);
});

ws.on('close', () => {
  console.log('\nConnection closed by server.');
  process.exit(0);
});

function streamFile(filePath) {
    return new Promise((resolve, reject) => {
        const args = [
            '-re',
            '-i', filePath,
            '-codec:a', 'libmp3lame',
            '-b:a', '128k',
            '-ar', '44100',
            '-ac', '1',
            '-f', 'mp3',
            'pipe:1'
        ];
        
        runFfmpeg(args, resolve, reject);
    });
}

function streamSilence(durationSec) {
     return new Promise((resolve, reject) => {
        const args = [
            '-re',
            '-f', 'lavfi',
            '-i', 'anullsrc=r=44100:cl=mono',
            '-t', durationSec.toString(),
            '-codec:a', 'libmp3lame',
            '-b:a', '128k',
            '-ar', '44100',
            '-ac', '1',
            '-f', 'mp3',
            'pipe:1'
        ];
        
        runFfmpeg(args, resolve, reject);
     });
}

function runFfmpeg(args, resolve, reject) {
    // console.log(`Debug: ffmpeg ${args.join(' ')}`);
    const ffmpeg = spawn('ffmpeg', args, { stdio: ['ignore', 'pipe', 'inherit'] });
    
    ffmpeg.stdout.on('data', data => {
        if (ws.readyState === WebSocket.OPEN) {
            ws.send(data);
        }
    });
    
    ffmpeg.on('close', (code) => {
        if (code === 0) resolve();
        else reject(new Error(`FFmpeg exited with code ${code}`));
    });
    
    ffmpeg.on('error', (err) => {
        reject(err);
    });
}