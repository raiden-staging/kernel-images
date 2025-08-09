import { Hono } from 'hono'
import { execCapture } from '../utils/exec.js'
import chalk from 'chalk'

export const clipboardRouter = new Hono()

// Check available clipboard tools on startup
const CLIPBOARD_TOOLS = {
  wayland: false,
  xclip: false
}

// Debug logging function
const debug = (message) => {
  if (process.env.DEBUG_LOGS) {
    console.log(chalk.cyan('[clipboard]'), message)
  }
}

// Initialize clipboard tools detection
async function detectClipboardTools() {
  try {
    // Check if wl-paste is available and works
    const wlCheck = await execCapture('bash', ['-lc', 'command -v wl-paste && wl-paste -t text 2>/dev/null'])
    CLIPBOARD_TOOLS.wayland = wlCheck.exitCode === 0
    
    // Check if xclip is available
    const xCheck = await execCapture('bash', ['-lc', 'command -v xclip'])
    CLIPBOARD_TOOLS.xclip = xCheck.exitCode === 0
    
    debug(`Detected clipboard tools: wayland=${CLIPBOARD_TOOLS.wayland}, xclip=${CLIPBOARD_TOOLS.xclip}`)
  } catch (error) {
    debug(`Error detecting clipboard tools: ${error.message}`)
  }
}

// Run detection on startup
detectClipboardTools()

async function wlGet() {
  debug('Attempting to get clipboard content using wl-paste')
  const txt = await execCapture('bash', ['-lc', 'wl-paste -t text 2>/dev/null || true'])
  if (txt.stdout?.length) return { type: 'text', text: txt.stdout.toString('utf8') }
  const png = await execCapture('bash', ['-lc', 'wl-paste -t image/png | base64 -w0 2>/dev/null || true'])
  if (png.stdout?.length) return { type: 'image', image_b64: png.stdout.toString('utf8'), image_mime: 'image/png' }
  return { type: 'text', text: '' }
}

async function wlSet({ type, text, image_b64, image_mime }) {
  debug(`Setting clipboard with wayland: type=${type}`)
  if (type === 'text') await execCapture('bash', ['-lc', `printf %s ${JSON.stringify(text || '')} | wl-copy`])
  else if (type === 'image' && image_b64) await execCapture('bash', ['-lc', `base64 -d | wl-copy -t ${image_mime || 'image/png'}`], { input: Buffer.from(image_b64, 'base64') })
}

async function xGet() {
  debug('Attempting to get clipboard content using xclip')
  const display = process.env.DISPLAY || ':20'
  const txt = await execCapture('bash', ['-lc', `DISPLAY=${display} xclip -selection clipboard -o 2>/dev/null || true`])
  return { type: 'text', text: txt.stdout.toString('utf8') }
}

async function xSet({ type, text }) {
  debug(`Setting clipboard with xclip: type=${type}`)
  const display = process.env.DISPLAY || ':20'
  if (type === 'text') await execCapture('bash', ['-lc', `printf %s ${JSON.stringify(text || '')} | DISPLAY=${display} xclip -selection clipboard`])
}

clipboardRouter.get('/clipboard', async (c) => {
  try {
    debug('GET /clipboard request received')
    let res = { type: 'text', text: '' }
    
    if (CLIPBOARD_TOOLS.wayland) {
      debug('Using wayland clipboard')
      res = await wlGet()
    } else if (CLIPBOARD_TOOLS.xclip) {
      debug('Using xclip clipboard')
      res = await xGet()
    } else {
      debug('No clipboard tools available')
    }
    
    return c.json(res)
  } catch (error) {
    debug(`Error in GET /clipboard: ${error.message}`)
    return c.json({ type: 'text', text: '' })
  }
})

clipboardRouter.post('/clipboard', async (c) => {
  try {
    debug('POST /clipboard request received')
    const body = await c.req.json()
    
    if (CLIPBOARD_TOOLS.wayland) {
      debug('Using wayland clipboard for setting content')
      await wlSet(body)
    } else if (CLIPBOARD_TOOLS.xclip) {
      debug('Using xclip clipboard for setting content')
      await xSet(body)
    } else {
      debug('No clipboard tools available for setting content')
      return c.json({ message: 'No clipboard tools available' }, 400)
    }
    
    return c.json({ ok: true })
  } catch (e) {
    debug(`Error in POST /clipboard: ${e.message}`)
    return c.json({ message: String(e.message) }, 400)
  }
})

clipboardRouter.get('/clipboard/stream', async (c) => {
  debug('GET /clipboard/stream request received')
  // simple poller
  let lastText = ''
  const stream = new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      const loop = async () => {
        try {
          let res = { type: 'text', text: '' }
          
          if (CLIPBOARD_TOOLS.wayland) {
            res = await wlGet()
          } else if (CLIPBOARD_TOOLS.xclip) {
            res = await xGet()
          }
          
          const curr = res.type === 'text' ? res.text : res.image_b64?.slice(0, 16) || ''
          if (curr !== lastText) {
            lastText = curr
            debug(`Clipboard content changed, sending update: ${res.type}`)
            controller.enqueue(enc.encode(`event: data\ndata: ${JSON.stringify({ ts: new Date().toISOString(), type: res.type, preview: res.type === 'text' ? (res.text || '').slice(0, 100) : 'image...' })}\n\n`))
          }
        } catch (error) {
          debug(`Error in clipboard stream: ${error.message}`)
        }
        setTimeout(loop, 1000)
      }
      loop()
    },
    cancel() {
      debug('Clipboard stream cancelled')
    }
  })
  return new Response(stream, { headers: { ...{ 'Cache-Control': 'no-cache' }, ...{ 'Content-Type': 'text/event-stream' }, 'X-SSE-Content-Type': 'application/json' } })
})
