// src/routes/browser-ext.js
// ESM. Requires: hono, undici, extract-zip, ws
import { Hono } from 'hono'
import fs from 'node:fs/promises'
import fssync from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'
import { spawn } from 'node:child_process'
import { setTimeout as delay } from 'node:timers/promises'
import extract from 'extract-zip'
import { request } from 'undici'
import { WebSocket } from 'ws'

export const browserExtRouter = new Hono()

// POST /browser/extension/add/unpacked  (multipart/form-data)
//   fields:
//     github_url: string   OR
//     archive_file: File (.zip, manifest at root or in first-level dir)
browserExtRouter.post('/browser/extension/add/unpacked', async (c) => {
  const origin = new URL(c.req.url).origin
  const form = await readForm(c)

  const params = {
    github_url: form.github_url || undefined,
    archive_file_path: form.archive_path || undefined,
    chromiumBinary: process.env.CHROMIUM_BINARY || 'chromium',
    devtoolsHost: process.env.CHROME_HOST || '127.0.0.1',
    devtoolsPort: Number(process.env.CHROME_PORT || 9222),
    policyDir: await detectPolicyDir([
      '/etc/chromium/policies/managed',
      '/etc/opt/chromium/policies/managed',
      '/etc/opt/chrome/policies/managed',
      '/etc/chrome/policies/managed'
    ]),
    repoStorageDir: process.env.EXT_REPO_DIR || '/opt/extrepo',
    repoBaseUrl: process.env.EXT_REPO_BASE_URL || `${origin}/extrepo`,
    keyStoreDir: process.env.EXT_KEY_STORE_DIR || '/var/lib/chrome-ext-keys',
    tryHotReloadPolicy: true,
    fallbackRestart: true,
    waitInstallTimeoutMs: 25000
  }

  try {
    const result = await addUnpackedExtension(params)
    return c.json(result, 201)
  } catch (err) {
    return c.json({ error: err.message || String(err) }, 500)
  } finally {
    if (form.cleanup) await form.cleanup().catch(() => { })
  }
})

// GET /extrepo/*  (serves CRX and update.xml from repoStorageDir)
browserExtRouter.get('/extrepo/*', async (c) => {
  const baseDir = process.env.EXT_REPO_DIR || '/opt/extrepo'
  const tail = c.req.path.replace(/^\/extrepo\/?/, '')
  const safePath = path.normalize(tail).replace(/^(\.\.[/\\])+/, '')
  const target = path.join(baseDir, safePath)
  if (!target.startsWith(path.resolve(baseDir))) return c.text('Forbidden', 403)
  try {
    const stat = await fs.stat(target)
    if (stat.isDirectory()) return c.text('Not Found', 404)
    const ext = path.extname(target).toLowerCase()
    const type =
      ext === '.xml' ? 'application/xml' :
        ext === '.crx' ? 'application/x-chrome-extension' :
          'application/octet-stream'
    const stream = fssync.createReadStream(target)
    return new Response(stream, { headers: { 'content-type': type } })
  } catch {
    return c.text('Not Found', 404)
  }
})

/* ───────────────────────────── form reader ───────────────────────────── */

async function readForm(c) {
  const ct = c.req.header('content-type') || ''
  if (ct.includes('application/json')) {
    const body = await c.req.json()
    return {
      github_url: body.github_url,
      archive_path: undefined,
      cleanup: async () => { }
    }
  }

  const fd = await c.req.formData()
  const github_url = fd.get('github_url')?.toString() || undefined
  const file = fd.get('archive_file')
  let archive_path
  let cleanup = async () => { }

  if (file && typeof file === 'object' && 'arrayBuffer' in file) {
    const tmpRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'extupload-'))
    archive_path = path.join(tmpRoot, 'upload.zip')
    const buf = Buffer.from(await file.arrayBuffer())
    await fs.writeFile(archive_path, buf)
    cleanup = async () => { try { await fs.rm(tmpRoot, { recursive: true, force: true }) } catch { } }
  }

  return { github_url, archive_path, cleanup }
}

/* ─────────────────────── core implementation ─────────────────────── */

const DEFAULTS = {
  chromiumBinary: process.env.CHROMIUM_BINARY || 'chromium',
  devtoolsHost: process.env.CHROME_HOST || '127.0.0.1',
  devtoolsPort: Number(process.env.CHROME_PORT || 9222),
  policyDir: '/etc/chromium/policies/managed',
  repoStorageDir: process.env.EXT_REPO_DIR || '/opt/extrepo',
  repoBaseUrl: process.env.EXT_REPO_BASE_URL || 'http://127.0.0.1:3000/extrepo',
  keyStoreDir: process.env.EXT_KEY_STORE_DIR || '/var/lib/chrome-ext-keys',
  userDataDirsProbe: [
    '/tmp/.chromium/chromium',
    '/home/kernel/.config/chromium',
    path.join(os.homedir(), '.config', 'chromium')
  ]
}

const NIBBLE_MAP = 'abcdefghijklmnop'.split('')

async function addUnpackedExtension({
  github_url,
  archive_file_path,
  chromiumBinary = DEFAULTS.chromiumBinary,
  devtoolsHost = DEFAULTS.devtoolsHost,
  devtoolsPort = DEFAULTS.devtoolsPort,
  policyDir = DEFAULTS.policyDir,
  repoStorageDir = DEFAULTS.repoStorageDir,
  repoBaseUrl = DEFAULTS.repoBaseUrl,
  keyStoreDir = DEFAULTS.keyStoreDir,
  tryHotReloadPolicy = true,
  fallbackRestart = true,
  waitInstallTimeoutMs = 25000
} = {}) {
  assertOneSource(github_url, archive_file_path)

  await ensureDir(repoStorageDir)
  await ensureDir(policyDir)
  await ensureDir(keyStoreDir)

  const workRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'extwork-'))
  const srcZip = path.join(workRoot, 'src.zip')
  const unpackDir = path.join(workRoot, 'unpacked')

  if (github_url) {
    const zipUrl = await resolveGithubZipURL(github_url)
    await downloadToFile(zipUrl, srcZip)
  } else {
    await fs.copyFile(archive_file_path, srcZip)
  }

  await extract(srcZip, { dir: unpackDir })
  const extRoot = await resolveExtensionRoot(unpackDir)
  const manifest = await readJson(path.join(extRoot, 'manifest.json'))
  validateManifest(manifest)

  const sourceKeyId = await decideKeyId({ github_url, manifest })
  const pemPath = path.join(keyStoreDir, `${sourceKeyId}.pem`)
  if (!fssync.existsSync(pemPath)) {
    await ensureDir(keyStoreDir)
    const { privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 })
    const pem = privateKey.export({ type: 'pkcs8', format: 'pem' })
    await fs.writeFile(pemPath, pem, { mode: 0o600 })
  }

  const outCrx = path.join(workRoot, 'packed.crx')
  await packWithChromium({ chromiumBinary, extRoot, pemPath, outCrx })

  const extId = await computeExtensionIdFromPem(pemPath)

  const publicDir = path.join(repoStorageDir, extId)
  await ensureDir(publicDir)
  const finalCrx = path.join(publicDir, `${extId}.crx`)
  await fs.copyFile(outCrx, finalCrx)
  const updateXmlPath = path.join(publicDir, 'update.xml')

  const codebaseUrl = `${trimSlash(repoBaseUrl)}/${extId}/${extId}.crx`
  const updateUrl = `${trimSlash(repoBaseUrl)}/${extId}/update.xml`
  await writeUpdateXml({ updateXmlPath, extId, version: manifest.version, codebaseUrl })

  const policyPath = path.join(policyDir, `force_${extId}.json`)
  await fs.writeFile(
    policyPath,
    JSON.stringify({ ExtensionInstallForcelist: [`${extId};${updateUrl}`] }, null, 2) + '\n',
    { mode: 0o644 }
  )

  const userDataDir = await locateUserDataDir(DEFAULTS.userDataDirsProbe)
  const extInstallDir = path.join(userDataDir, 'Default', 'Extensions', extId)
  const installedBefore = await dirExists(extInstallDir)

  if (tryHotReloadPolicy) {
    await devtoolsReloadPolicies({ devtoolsHost, devtoolsPort }).catch(() => { })
  }

  let installed = await waitForExtensionInstallOnDisk(extInstallDir, waitInstallTimeoutMs)
  if (!installed && fallbackRestart) {
    await devtoolsRestartBrowser({ devtoolsHost, devtoolsPort }).catch(() => { })
    await waitDevToolsUp({ devtoolsHost, devtoolsPort, timeoutMs: 20000 }).catch(() => { })
    installed = await waitForExtensionInstallOnDisk(extInstallDir, 15000)
  }

  await safeRm(workRoot)

  return {
    id: extId,
    version: manifest.version,
    crx_path: finalCrx,
    update_xml_path: updateXmlPath,
    update_url: updateUrl,
    policy_path: policyPath,
    installed: installed || installedBefore || false,
    profile_extensions_dir: extInstallDir
  }
}

/* ────────────────────────────── helpers ────────────────────────────── */

function assertOneSource(github_url, archive_file_path) {
  const provided = [!!github_url, !!archive_file_path].filter(Boolean).length
  if (provided !== 1) throw new Error('Provide exactly one of github_url or archive_file')
}

async function ensureDir(p) {
  await fs.mkdir(p, { recursive: true })
}

async function safeRm(p) {
  try { await fs.rm(p, { recursive: true, force: true }) } catch { }
}

async function detectPolicyDir(candidates) {
  for (const dir of candidates) {
    try {
      await fs.mkdir(dir, { recursive: true })
      const test = path.join(dir, '.write_test')
      await fs.writeFile(test, 'x')
      await fs.rm(test)
      return dir
    } catch { }
  }
  // last resort still return first path; write will error explicitly if not writable
  return candidates[0]
}

async function downloadToFile(url, outPath) {
  let res = await request(url, { maxRedirections: 3 }).catch(() => null)
  if (!res || res.statusCode < 200 || res.statusCode >= 300) {
    throw new Error(`Download failed ${res ? res.statusCode : 'net'} for ${url}`)
  }
  const file = fssync.createWriteStream(outPath)
  await new Promise((resolve, reject) => {
    res.body.pipe(file)
    res.body.on('error', reject)
    file.on('finish', resolve)
  })
}

async function resolveGithubZipURL(input) {
  const u = new URL(input)
  if (u.hostname !== 'github.com') throw new Error('github_url must be github.com')
  const parts = u.pathname.split('/').filter(Boolean)
  if (parts.length < 2) throw new Error('Invalid GitHub repo URL')

  if (parts.includes('archive') && parts.includes('refs') && parts.includes('heads')) {
    return String(u)
  }

  const treeIdx = parts.indexOf('tree')
  let branch = null
  if (treeIdx >= 0 && parts[treeIdx + 1]) branch = parts[treeIdx + 1]

  const [owner, repo] = parts
  const tries = []
  if (branch) {
    tries.push(`https://codeload.github.com/${owner}/${repo}/zip/refs/heads/${branch}`)
  } else {
    tries.push(`https://codeload.github.com/${owner}/${repo}/zip/refs/heads/main`)
    tries.push(`https://codeload.github.com/${owner}/${repo}/zip/refs/heads/master`)
  }

  for (const url of tries) {
    const head = await request(url, { method: 'HEAD' }).catch(() => null)
    if (head && head.statusCode === 200) return url
  }
  return `https://codeload.github.com/${owner}/${repo}/zip/HEAD`
}

async function resolveExtensionRoot(unpackedDir) {
  if (await fileExists(path.join(unpackedDir, 'manifest.json'))) return unpackedDir
  const entries = await fs.readdir(unpackedDir, { withFileTypes: true })
  const dirs = entries.filter((e) => e.isDirectory())
  if (dirs.length === 1) {
    const cand = path.join(unpackedDir, dirs[0].name)
    if (await fileExists(path.join(cand, 'manifest.json'))) return cand
  }
  for (const e of entries) {
    if (!e.isDirectory()) continue
    const cand = path.join(unpackedDir, e.name)
    if (await fileExists(path.join(cand, 'manifest.json'))) return cand
  }
  throw new Error('manifest.json not found')
}

async function readJson(p) {
  const buf = await fs.readFile(p)
  return JSON.parse(buf.toString('utf8'))
}

function validateManifest(m) {
  if (!m || typeof m !== 'object') throw new Error('Invalid manifest.json')
  if (!m.manifest_version) throw new Error('manifest_version missing')
  if (m.manifest_version !== 3) throw new Error('Only Manifest V3 is supported')
  if (!m.version) throw new Error('manifest version missing')
  if (!/^\d+(\.\d+){0,3}$/.test(m.version)) throw new Error('manifest version must be dotted number')
}

async function decideKeyId({ github_url, manifest }) {
  if (github_url) {
    const norm = github_url.replace(/\.git$/, '').toLowerCase()
    return 'gh_' + crypto.createHash('sha256').update(norm).digest('hex').slice(0, 16)
  }
  const name = String(manifest.name || 'uploaded').toLowerCase()
  return 'up_' + crypto.createHash('sha256').update(name).digest('hex').slice(0, 16)
}

async function lookupUser(name) {
  const txt = await fs.readFile('/etc/passwd', 'utf8')
  const line = txt.split('\n').find(l => l.startsWith(name + ':'))
  if (!line) throw new Error(`User not found: ${name}`)
  const parts = line.split(':')
  return { uid: Number(parts[2]), gid: Number(parts[3]), home: parts[5] }
}

async function packWithChromium({ chromiumBinary, extRoot, pemPath, outCrx }) {
  const args = [`--no-sandbox`, `--pack-extension=${extRoot}`, `--pack-extension-key=${pemPath}`]
  const { uid, gid, home } = await lookupUser(process.env.PACK_AS_USER || 'kernel')
  // ensure the packer can read the private key
  try { await fs.chown(pemPath, uid, gid) } catch { }
  await new Promise((resolve, reject) => {
    const p = spawn(chromiumBinary, args, {
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, HOME: home, DISPLAY: process.env.DISPLAY || ':1' },
      uid, gid
    })
    let stderr = ''
    p.stderr.on('data', d => { stderr += String(d) })
    p.on('error', reject)
    p.on('close', code => code === 0 ? resolve() : reject(new Error(`${chromiumBinary} ${args.join(' ')} exited ${code}\n${stderr}`)))
  })
  const produced = `${extRoot}.crx`
  if (!fssync.existsSync(produced)) throw new Error('Chromium packer did not create .crx')
  await fs.copyFile(produced, outCrx)
}

async function computeExtensionIdFromPem(pemPath) {
  const pem = await fs.readFile(pemPath, 'utf8')
  const keyObj = crypto.createPrivateKey(pem)
  const pub = crypto.createPublicKey(keyObj)
  const spkiDer = pub.export({ type: 'spki', format: 'der' })
  const hash = crypto.createHash('sha256').update(spkiDer).digest()
  const first16 = hash.subarray(0, 16)
  let id = ''
  for (const b of first16) id += NIBBLE_MAP[(b >> 4) & 0x0f] + NIBBLE_MAP[b & 0x0f]
  return id
}

async function writeUpdateXml({ updateXmlPath, extId, version, codebaseUrl }) {
  const xml =
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<gupdate xmlns="http://www.google.com/update2/response" protocol="2.0">\n` +
    `  <app appid="${extId}">\n` +
    `    <updatecheck codebase="${codebaseUrl}" version="${version}"/>\n` +
    `  </app>\n` +
    `</gupdate>\n`
  await fs.writeFile(updateXmlPath, xml, { mode: 0o644 })
}

function trimSlash(s) { return s.replace(/\/+$/, '') }

async function execFileStrict(cmd, args = [], opts = {}) {
  await new Promise((resolve, reject) => {
    const p = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], ...opts })
    let stderr = ''
    p.stderr.on('data', (d) => { stderr += d.toString() })
    p.on('error', reject)
    p.on('close', (code) => {
      if (code === 0) resolve()
      else reject(new Error(`${cmd} ${args.join(' ')} exited ${code}\n${stderr}`))
    })
  })
}

async function fileExists(p) {
  try { await fs.access(p); return true } catch { return false }
}

async function dirExists(p) {
  try { const st = await fs.stat(p); return st.isDirectory() } catch { return false }
}

async function locateUserDataDir(candidates) {
  for (const cand of candidates) if (await dirExists(cand)) return cand
  return candidates[0]
}

/* ───────── DevTools helpers ───────── */

async function getBrowserWsUrl({ devtoolsHost, devtoolsPort }) {
  const url = `http://${devtoolsHost}:${devtoolsPort}/json/version`
  const res = await request(url)
  if (res.statusCode !== 200) throw new Error('DevTools not reachable')
  const data = await res.body.json()
  if (!data.webSocketDebuggerUrl) throw new Error('webSocketDebuggerUrl missing')
  return data.webSocketDebuggerUrl
}

async function cdpSession(wsUrl) {
  const ws = new WebSocket(wsUrl, { perMessageDeflate: false })
  await new Promise((resolve, reject) => {
    ws.once('open', resolve)
    ws.once('error', reject)
  })
  let id = 0
  const pending = new Map()
  ws.on('message', (data) => {
    const msg = JSON.parse(String(data))
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id)
      pending.delete(msg.id)
      if (msg.error) reject(new Error(msg.error.message || 'CDP error'))
      else resolve(msg.result)
    }
  })
  function call(method, params) {
    return new Promise((resolve, reject) => {
      const msg = { id: ++id, method, params }
      pending.set(id, { resolve, reject })
      ws.send(JSON.stringify(msg), (err) => err && reject(err))
    })
  }
  function close() { try { ws.close() } catch { } }
  return { call, close, ws }
}

async function devtoolsReloadPolicies({ devtoolsHost, devtoolsPort }) {
  const wsUrl = await getBrowserWsUrl({ devtoolsHost, devtoolsPort })
  const cdp = await cdpSession(wsUrl)
  try {
    const { targetId } = await cdp.call('Target.createTarget', { url: 'chrome://policy' })
    const { sessionId } = await cdp.call('Target.attachToTarget', { targetId, flatten: true })
    await cdp.call('Runtime.enable', { sessionId })
    await cdp.call('Runtime.evaluate', {
      sessionId,
      expression: 'chrome && chrome.send ? chrome.send("reloadPolicies") : null',
      awaitPromise: false
    })
    await delay(1000)
    await cdp.call('Target.closeTarget', { targetId })
  } finally {
    cdp.close()
  }
}

async function devtoolsRestartBrowser({ devtoolsHost, devtoolsPort }) {
  const wsUrl = await getBrowserWsUrl({ devtoolsHost, devtoolsPort })
  const cdp = await cdpSession(wsUrl)
  try {
    await cdp.call('Target.createTarget', { url: 'chrome://restart' })
  } finally {
    cdp.close()
  }
}

async function waitDevToolsUp({ devtoolsHost, devtoolsPort, timeoutMs }) {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    try {
      const wsUrl = await getBrowserWsUrl({ devtoolsHost, devtoolsPort })
      if (wsUrl) return true
    } catch { }
    await delay(500)
  }
  return false
}

async function waitForExtensionInstallOnDisk(extInstallDir, timeoutMs) {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (await dirExists(extInstallDir)) {
      const subs = await fs.readdir(extInstallDir).catch(() => [])
      if (subs.length > 0) return true
    }
    await delay(500)
  }
  return false
}
