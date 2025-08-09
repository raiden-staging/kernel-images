import { execSync } from 'node:child_process'
import { request, fetch } from 'undici'
import { Readable } from 'node:stream'
import 'dotenv/config'
import chalk from 'chalk'

// Hardcoded BASE URL using PORT from environment
export const BASE = () => `http://localhost:${process.env.PORT || '9999'}`

export function hasCmd(cmd) {
  try {
    execSync(`bash -lc "command -v ${cmd} >/dev/null 2>&1"`, { stdio: 'ignore' })
    return tru
  } catch {
    return false
  }
}

export async function j(url, init = {}) {
  const fullUrl = `${BASE()}${url}`
  
  if (process.env.DEBUG_LOGS === 'true') {
    // Generate curl equivalent
    let curlCmd = `curl -X ${init.method || 'GET'} "${fullUrl}"`
    
    // Add headers
    const headers = { 'content-type': 'application/json', ...(init.headers || {}) }
    Object.entries(headers).forEach(([key, value]) => {
      curlCmd += ` -H "${key}: ${value}"`
    })
    
    // Add body if present
    if (init.body) {
      curlCmd += ` -d '${init.body}'`
    }
    
    console.log(chalk.cyan('🔍 Request:'), chalk.yellow(curlCmd))
  }
  
  const r = await fetch(fullUrl, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init.headers || {}) }
  })
  
  const txt = await r.text()
  let result
  
  try {
    result = { status: r.status, body: JSON.parse(txt) }
  } catch {
    result = { status: r.status, body: txt }
  }
  
  if (process.env.DEBUG_LOGS === 'true') {
    console.log(chalk.cyan('📡 Response:'), 
      chalk.green(`Status: ${r.status}`), 
      chalk.magenta('\nBody:'), 
      typeof result.body === 'object' ? 
        chalk.yellow(JSON.stringify(result.body, null, 2)) : 
        chalk.yellow(result.body)
    )
  }
  
  return result
}

export async function raw(url, init = {}) {
  const fullUrl = `${BASE()}${url}`
  
  if (process.env.DEBUG_LOGS === 'true') {
    // Generate curl equivalent
    let curlCmd = `curl -X ${init.method || 'GET'} "${fullUrl}"`
    
    // Add headers
    if (init.headers) {
      Object.entries(init.headers).forEach(([key, value]) => {
        curlCmd += ` -H "${key}: ${value}"`
      })
    }
    
    // Add body if present
    if (init.body) {
      curlCmd += ` -d '${init.body}'`
    }
    
    console.log(chalk.cyan('🔍 Raw Request:'), chalk.yellow(curlCmd))
  }
  
  const response = await fetch(fullUrl, init)
  
  if (process.env.DEBUG_LOGS === 'true') {
    console.log(chalk.cyan('📡 Raw Response:'), chalk.green(`Status: ${response.status}`))
  }
  
  return response
}

// Minimal SSE reader: returns first N events parsed as JSON
export async function readSSE(path, { max = 1, timeoutMs = 5000 } = {}) {
  const res = await fetch(`${BASE()}${path}`)
  if (!res.ok) throw new Error(`bad status ${res.status}`)
  const reader = res.body.getReader()
  const decoder = new TextDecoder('utf-8')
  const events = []
  let buf = ''
  const start = Date.now()

  while (events.length < max && Date.now() - start < timeoutMs) {
    const { value, done } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let idx
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const chunk = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      const dataLine = chunk.split('\n').find(l => l.startsWith('data: '))
      if (dataLine) {
        const json = dataLine.slice(6)
        try { events.push(JSON.parse(json)) } catch {}
      }
      if (events.length >= max) break
    }
  }
  try { reader.cancel() } catch {}
  return events
}

// helper to create random path under /tmp
export function tmpPath(name = 'kco') {
  const id = Math.random().toString(36).slice(2)
  return `/tmp/${name}-${id}`
}
