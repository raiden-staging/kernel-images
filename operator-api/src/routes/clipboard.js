import { Hono } from 'hono'
import { execCapture } from '../utils/exec.js'

export const clipboardRouter = new Hono()

async function wlGet() {
  const txt = await execCapture('bash', ['-lc', 'command -v wl-paste >/dev/null && wl-paste -t text || true'])
  if (txt.stdout?.length) return { type: 'text', text: txt.stdout.toString('utf8') }
  const png = await execCapture('bash', ['-lc', 'command -v wl-paste >/dev/null && wl-paste -t image/png | base64 -w0 || true'])
  if (png.stdout?.length) return { type: 'image', image_b64: png.stdout.toString('utf8'), image_mime: 'image/png' }
  return { type: 'text', text: '' }
}

async function wlSet({ type, text, image_b64, image_mime }) {
  if (type === 'text') await execCapture('bash', ['-lc', `printf %s ${JSON.stringify(text || '')} | wl-copy`])
  else if (type === 'image' && image_b64) await execCapture('bash', ['-lc', `base64 -d | wl-copy -t ${image_mime || 'image/png'}`], { input: Buffer.from(image_b64, 'base64') })
}

async function xGet() {
  const txt = await execCapture('bash', ['-lc', 'command -v xclip >/dev/null && xclip -selection clipboard -o || true'])
  return { type: 'text', text: txt.stdout.toString('utf8') }
}

async function xSet({ type, text }) {
  if (type === 'text') await execCapture('bash', ['-lc', `printf %s ${JSON.stringify(text || '')} | xclip -selection clipboard`])
}

clipboardRouter.get('/clipboard', async (c) => {
  try {
    const wayland = process.env.XDG_SESSION_TYPE === 'wayland'
    const res = wayland ? await wlGet() : await xGet()
    return c.json(res)
  } catch {
    return c.json({ type: 'text', text: '' })
  }
})

clipboardRouter.post('/clipboard', async (c) => {
  try {
    const body = await c.req.json()
    const wayland = process.env.XDG_SESSION_TYPE === 'wayland'
    if (wayland) await wlSet(body)
    else await xSet(body)
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

clipboardRouter.get('/clipboard/stream', async (c) => {
  // simple poller
  let lastText = ''
  const stream = new ReadableStream({
    async start(controller) {
      const enc = new TextEncoder()
      const loop = async () => {
        try {
          const res = await (process.env.XDG_SESSION_TYPE === 'wayland' ? wlGet() : xGet())
          const curr = res.type === 'text' ? res.text : res.image_b64?.slice(0, 16) || ''
          if (curr !== lastText) {
            lastText = curr
            controller.enqueue(enc.encode(`event: data\ndata: ${JSON.stringify({ ts: new Date().toISOString(), type: res.type, preview: res.type === 'text' ? (res.text || '').slice(0, 100) : 'image...' })}\n\n`))
          }
        } catch {}
        setTimeout(loop, 1000)
      }
      loop()
    },
    cancel() {}
  })
  return new Response(stream, { headers: { ...{ 'Cache-Control': 'no-cache' }, ...{ 'Content-Type': 'text/event-stream' }, 'X-SSE-Content-Type': 'application/json' } })
})
