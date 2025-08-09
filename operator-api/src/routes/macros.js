import { Hono } from 'hono'
import { uid } from '../utils/ids.js'
import { execCapture } from '../utils/exec.js'

const macros = new Map() // id -> {name, steps}

export const macrosRouter = new Hono()

macrosRouter.post('/macros/create', async (c) => {
  const body = await c.req.json()
  if (!body?.name || !Array.isArray(body.steps)) return c.json({ message: 'Bad Request' }, 400)
  const id = uid()
  macros.set(id, { macro_id: id, name: body.name, steps: body.steps })
  return c.json({ macro_id: id })
})

macrosRouter.post('/macros/run', async (c) => {
  const body = await c.req.json()
  const item = [...macros.values()].find((m) => m.macro_id === body.macro_id)
  if (!item) return c.json({ message: 'Not Found' }, 404)
  const run_id = uid()
  ;(async () => {
    for (const step of item.steps) {
      if (step.action === 'keyboard.type' && step.text) await execCapture('xdotool', ['type', '--', step.text])
      else if (step.action === 'keyboard.key' && step.key) await execCapture('xdotool', ['key', step.key])
      else if (step.action === 'sleep' && step.ms) await new Promise((r) => setTimeout(r, step.ms))
    }
  })()
  return c.json({ started: true, run_id })
})

macrosRouter.get('/macros/list', async (c) => {
  return c.json({ items: [...macros.values()].map((m) => ({ macro_id: m.macro_id, name: m.name, steps_count: m.steps.length })) })
})

macrosRouter.delete('/macros/:macro_id', async (c) => {
  const id = c.req.param('macro_id')
  if (!macros.has(id)) return c.json({ message: 'Not Found' }, 404)
  macros.delete(id)
  return c.json({ ok: true })
})
