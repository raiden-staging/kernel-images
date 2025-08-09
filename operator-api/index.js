import 'dotenv/config'
import { serve } from '@hono/node-server'
import { Hono } from 'hono'
import { cors } from 'hono/cors'
import morgan from 'morgan'
import { app as api } from './src/app.js'

// Debug environment variables in a structured way
console.log('🔧 [DEBUG] process.env 🔧')
console.log('┌─────────────────────────────────────────────────────')
Object.keys(process.env)
  .sort()
  .forEach(key => {
    // Mask sensitive values
    const isSensitive = /key|token|secret|password|auth/i.test(key)
    const value = isSensitive 
      ? `${process.env[key].substring(0, 3)}${'*'.repeat(6)}` 
      : process.env[key]
    console.log(`│ ${key.padEnd(25)} │ ${value}`)
  })
console.log('└─────────────────────────────────────────────────────')


const port = Number(process.env.PORT || 9999)

const root = new Hono()
root.use('*', cors())
root.use('*', async (c, next) => {
  // minimal morgan-like logging
  const start = Date.now()
  await next()
  const ms = Date.now() - start
  console.log(`${c.req.method} ${c.req.path} -> ${c.res.status} ${ms}ms`)
})

root.route('/', api)

serve({
  fetch: root.fetch,
  port
})

console.log(`Kernel Computer Operator API listening on :${port}`)
