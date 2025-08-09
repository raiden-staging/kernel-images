import { spawn } from 'node:child_process'

export function execSpawn(cmd, args = [], opts = {}) {
  const child = spawn(cmd, args, { shell: false, ...opts })
  return child
}

export async function execCapture(cmd, args = [], opts = {}) {
  return new Promise((resolve) => {
    const child = execSpawn(cmd, args, opts)
    let stdout = Buffer.alloc(0)
    let stderr = Buffer.alloc(0)
    child.stdout?.on('data', (d) => (stdout = Buffer.concat([stdout, d])))
    child.stderr?.on('data', (d) => (stderr = Buffer.concat([stderr, d])))
    child.on('close', (code) => resolve({ code, stdout, stderr }))
  })
}
