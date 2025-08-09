import { spawn } from 'node:child_process'

export function execSpawn(cmd, args = [], opts = {}) {
  console.log(`[EXEC] Spawning command: ${cmd} ${args.join(' ')}`)
  const child = spawn(cmd, args, { shell: false, ...opts })
  return child
}

export async function execCapture(cmd, args = [], opts = {}) {
  console.log(`[EXEC] Capturing output from: ${cmd} ${args.join(' ')}`)
  return new Promise((resolve) => {
    const child = execSpawn(cmd, args, opts)
    let stdout = Buffer.alloc(0)
    let stderr = Buffer.alloc(0)
    
    child.stdout?.on('data', (d) => {
      console.log(`[EXEC] [STDOUT] Received ${d.length} bytes`)
      stdout = Buffer.concat([stdout, d])
    })
    
    child.stderr?.on('data', (d) => {
      console.log(`[EXEC] [STDERR] Received ${d.length} bytes`)
      stderr = Buffer.concat([stderr, d])
    })
    
    child.on('close', (code) => {
      console.log(`[EXEC] Command completed with exit code: ${code}`)
      console.log(`[EXEC] Total stdout: ${stdout.length} bytes, stderr: ${stderr.length} bytes`)
      
      if (stdout.length > 0) {
        const preview = stdout.toString().substring(0, 100)
        console.log(`[EXEC] [STDOUT] Preview: ${preview}${stdout.length > 100 ? '...' : ''}`)
      }
      
      if (stderr.length > 0) {
        const preview = stderr.toString().substring(0, 100)
        console.log(`[EXEC] [STDERR] Preview: ${preview}${stderr.length > 100 ? '...' : ''}`)
      }
      
      resolve({ code, stdout, stderr })
    })
  })
}
