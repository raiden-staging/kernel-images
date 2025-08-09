import fs from 'node:fs'
import path from 'node:path'
import { execSpawn } from '../utils/exec.js'
import { RECORDINGS_DIR } from '../utils/env.js'
import { uid } from '../utils/ids.js'

const FFMPEG = process.env.FFMPEG_BIN || 'ffmpeg'
const SCREEN_WIDTH = Number(process.env.SCREEN_WIDTH || 1280)
const SCREEN_HEIGHT = Number(process.env.SCREEN_HEIGHT || 720)
const DISPLAY = process.env.DISPLAY || ':0'

const state = new Map() // id -> {proc, file, started_at, finished_at}

function buildArgsMp4({ framerate = 20, maxDurationInSeconds }) {
  const input = ['-f', 'x11grab', '-video_size', `${SCREEN_WIDTH}x${SCREEN_HEIGHT}`, '-i', `${DISPLAY}.0`]
  const common = ['-r', String(framerate), '-vcodec', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-movflags', '+faststart']
  if (maxDurationInSeconds) common.push('-t', String(maxDurationInSeconds))
  return [...input, ...common]
}

export function startRecording({ id, framerate, maxDurationInSeconds, maxFileSizeInMB }) {
  const recId = id || 'default'
  if (state.get(recId)?.proc) throw new Error('Already recording')
  const fileName = `${recId}-${Date.now()}.mp4`
  const outPath = path.join(RECORDINGS_DIR, fileName)
  const args = buildArgsMp4({ framerate, maxDurationInSeconds })
  if (maxFileSizeInMB) args.push('-fs', String(maxFileSizeInMB * 1024 * 1024))
  args.push(outPath)
  const proc = execSpawn(FFMPEG, ['-y', ...args], { env: { ...process.env } })
  const started_at = new Date().toISOString()
  state.set(recId, { proc, file: outPath, started_at, finished_at: null })
  proc.on('close', () => {
    const rec = state.get(recId)
    if (rec) {
      rec.finished_at = new Date().toISOString()
      rec.proc = null
      state.set(recId, rec)
    }
  })
  return { id: recId, file: outPath, started_at }
}

export function stopRecording({ id = 'default', forceStop = false }) {
  const rec = state.get(id)
  if (!rec?.proc) throw new Error('Not recording')
  if (forceStop) rec.proc.kill('SIGKILL')
  else rec.proc.kill('SIGINT')
  return true
}

export function getRecorderInfoList() {
  const items = []
  for (const [id, r] of state.entries()) {
    items.push({
      id,
      isRecording: Boolean(r.proc),
      started_at: r.started_at || null,
      finished_at: r.finished_at || null
    })
  }
  if (!items.length) {
    items.push({ id: 'default', isRecording: false, started_at: null, finished_at: null })
  }
  return items
}

export function getLatestFilePath({ id }) {
  const rec = state.get(id || 'default')
  if (!rec) return null
  return rec.file
}

export function isRecording({ id }) {
  const rec = state.get(id || 'default')
  return Boolean(rec?.proc)
}

export function deleteRecording({ id }) {
  const rec = state.get(id || 'default')
  if (!rec?.file) throw new Error('Not found')
  fs.rmSync(rec.file, { force: true })
  return true
}
