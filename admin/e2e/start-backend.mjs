import { mkdirSync, rmSync } from 'node:fs'
import { spawn, spawnSync } from 'node:child_process'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const adminDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const backendDir = path.resolve(adminDir, '..', 'backend')
const tempDir = path.resolve(backendDir, 'tmp', 'admin-e2e')
const allowedRoot = path.resolve(backendDir, 'tmp') + path.sep
if (!tempDir.startsWith(allowedRoot)) throw new Error(`unsafe E2E temp path: ${tempDir}`)

rmSync(tempDir, { recursive: true, force: true })
mkdirSync(tempDir, { recursive: true })

const executable = path.join(tempDir, process.platform === 'win32' ? 'ticket-e2e.exe' : 'ticket-e2e')
const buildEnv = { ...process.env, CGO_ENABLED: '1' }
if (process.platform === 'win32') {
  const pathKey = Object.keys(buildEnv).find(key => key.toLowerCase() === 'path') || 'PATH'
  buildEnv[pathKey] = `C:\\msys64\\ucrt64\\bin${path.delimiter}${buildEnv[pathKey] || ''}`
}
const build = spawnSync('go', ['build', '-o', executable, './cmd'], {
  cwd: backendDir,
  env: buildEnv,
  stdio: 'inherit',
})
if (build.error) console.error(build.error)
if (build.status !== 0) process.exit(build.status ?? 1)

const child = spawn(executable, [], {
  cwd: backendDir,
  env: {
    ...process.env,
    TICKET_SERVER_PORT: '19180',
    TICKET_SERVER_MODE: 'release',
    TICKET_SERVER_CORS_ALLOWED_ORIGINS: 'http://127.0.0.1:4173',
    TICKET_DATABASE_PATH: path.join(tempDir, 'ticket-system.db'),
    TICKET_SECURITY_KEY_FILE: path.join(tempDir, 'instance-key.json'),
    TICKET_BACKUP_DIRECTORY: path.join(tempDir, 'backups'),
    TICKET_BOOTSTRAP_ADMIN_PASSWORD: 'Supplier-E2E-Password-1',
    TICKET_BOOTSTRAP_PLATFORM_USERNAME: 'platform-e2e',
    TICKET_BOOTSTRAP_PLATFORM_PASSWORD: 'Platform-E2E-Password-2',
  },
  stdio: 'inherit',
})

let stopping = false
const stop = signal => {
  if (stopping) return
  stopping = true
  child.kill(signal)
}
process.on('SIGINT', () => stop('SIGINT'))
process.on('SIGTERM', () => stop('SIGTERM'))
child.on('exit', code => process.exit(code ?? 0))
