import { Hono } from 'hono'
import { execCapture } from '../utils/exec.js'

export const inputRouter = new Hono()

async function runXdotool(args) {
  const res = await execCapture('xdotool', args)
  if (res.code !== 0) throw new Error(res.stderr.toString('utf8') || 'xdotool error')
}

inputRouter.post('/computer/move_mouse', async (c) => {
  try {
    const { x, y } = await c.req.json()
    if (typeof x !== 'number' || typeof y !== 'number') return c.json({ message: 'Invalid coords' }, 400)
    await runXdotool(['mousemove', String(x), String(y)])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/computer/click_mouse', async (c) => {
  try {
    const body = await c.req.json()
    const buttonMap = { left: 1, middle: 2, right: 3, back: 8, forward: 9 }
    const b = body.button ? buttonMap[body.button] || buttonMap.left : buttonMap.left
    const clickType = body.click_type || 'click'
    const count = body.num_clicks || 1
    if (clickType === 'down') await runXdotool(['mousedown', String(b)])
    else if (clickType === 'up') await runXdotool(['mouseup', String(b)])
    else await runXdotool(['click', '--repeat', String(count), String(b)])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/mouse/move', async (c) => inputRouter.routes.get('/computer/move_mouse')(c))
inputRouter.post('/input/mouse/click', async (c) => {
  try {
    const body = await c.req.json()
    const button = typeof body.button === 'number' ? String(body.button) : String({ left: 1, middle: 2, right: 3 }[body.button] || 1)
    const count = body.count || 1
    await runXdotool(['click', '--repeat', String(count), button])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/mouse/scroll', async (c) => {
  try {
    const { dx = 0, dy = -120 } = await c.req.json()
    // xdotool mouse wheel: 4 up, 5 down, 6 left, 7 right
    const verticalClicks = Math.max(1, Math.round(Math.abs(dy) / 120))
    const horizontalClicks = Math.max(0, Math.round(Math.abs(dx) / 120))
    const vButton = dy < 0 ? '5' : '4'
    const hButton = dx > 0 ? '7' : '6'
    for (let i = 0; i < verticalClicks; i++) await runXdotool(['click', vButton])
    for (let i = 0; i < horizontalClicks; i++) await runXdotool(['click', hButton])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/keyboard/type', async (c) => {
  try {
    const { text, wpm = 300, enter = false } = await c.req.json()
    if (typeof text !== 'string') return c.json({ message: 'Missing text' }, 400)
    const delay = Math.max(1, Math.round(60000 / (wpm * 5))) // ms per char
    await runXdotool(['type', '--delay', String(delay), '--clearmodifiers', '--', text])
    if (enter) await runXdotool(['key', 'Return'])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/keyboard/key_down', async (c) => {
  try {
    const { key } = await c.req.json()
    await runXdotool(['keydown', key])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/keyboard/key_up', async (c) => {
  try {
    const { key } = await c.req.json()
    await runXdotool(['keyup', key])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

function matchToSearchArgs(match) {
  const args = []
  if (match?.title_contains) args.push('--name', match.title_contains)
  if (match?.class) args.push('--class', match.class)
  if (match?.pid) args.push('--pid', String(match.pid))
  return args.length ? args : ['--onlyvisible', '.']
}

inputRouter.post('/input/window/activate', async (c) => {
  try {
    const { match } = await c.req.json()
    const args = ['search', ...matchToSearchArgs(match)]
    const found = await execCapture('xdotool', args)
    const wid = found.stdout.toString('utf8').split('\n').filter(Boolean)[0]
    if (!wid) return c.json({ activated: false })
    await execCapture('xdotool', ['windowactivate', wid])
    return c.json({ activated: true })
  } catch {
    return c.json({ activated: false })
  }
})

inputRouter.post('/input/window/move_resize', async (c) => {
  try {
    const { match, x, y, width, height } = await c.req.json()
    const found = await execCapture('xdotool', ['search', ...matchToSearchArgs(match)])
    const wid = found.stdout.toString('utf8').split('\n').filter(Boolean)[0]
    if (!wid) return c.json({ message: 'Not Found' }, 404)
    await execCapture('xdotool', ['windowmove', wid, String(x), String(y)])
    await execCapture('xdotool', ['windowsize', wid, String(width), String(height)])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})

inputRouter.post('/input/window/close', async (c) => {
  try {
    const { match } = await c.req.json()
    const found = await execCapture('xdotool', ['search', ...matchToSearchArgs(match)])
    const wid = found.stdout.toString('utf8').split('\n').filter(Boolean)[0]
    if (!wid) return c.json({ message: 'Not Found' }, 404)
    await execCapture('xdotool', ['windowclose', wid])
    return c.json({ ok: true })
  } catch (e) {
    return c.json({ message: String(e.message) }, 400)
  }
})
