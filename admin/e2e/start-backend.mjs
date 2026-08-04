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
const buildEnv = { ...process.env, CGO_ENABLED: '0' }
const build = spawnSync('go', ['build', '-o', executable, './cmd'], {
  cwd: backendDir,
  env: buildEnv,
  stdio: 'inherit',
})
if (build.error) console.error(build.error)
if (build.status !== 0) process.exit(build.status ?? 1)

const databaseName = `ticket_admin_e2e_${process.pid}_${Date.now()}`
const postgresHost = process.env.TICKET_TEST_POSTGRES_HOST || process.env.PGHOST || '127.0.0.1'
const postgresPort = process.env.TICKET_TEST_POSTGRES_PORT || process.env.PGPORT || '5432'
const postgresUser = process.env.TICKET_TEST_POSTGRES_USER || process.env.PGUSER || 'postgres'
const databaseArgs = ['-h', postgresHost, '-p', postgresPort, '-U', postgresUser]
const createDatabase = spawnSync('createdb', [...databaseArgs, databaseName], { env: process.env, stdio: 'inherit' })
if (createDatabase.error) console.error(createDatabase.error)
if (createDatabase.status !== 0) process.exit(createDatabase.status ?? 1)

const child = spawn(executable, [], {
  cwd: backendDir,
  env: {
    ...process.env,
    TICKET_SERVER_PORT: '19180',
    TICKET_SERVER_MODE: 'release',
    TICKET_SERVER_CORS_ALLOWED_ORIGINS: 'http://127.0.0.1:4173',
    TICKET_DATABASE_DRIVER: 'postgres',
    TICKET_DATABASE_HOST: postgresHost,
    TICKET_DATABASE_PORT: postgresPort,
    TICKET_DATABASE_NAME: databaseName,
    TICKET_DATABASE_USER: postgresUser,
    TICKET_DATABASE_PASSWORD: process.env.PGPASSWORD || '',
    TICKET_DATABASE_SSLMODE: process.env.TICKET_TEST_POSTGRES_SSLMODE || 'disable',
    TICKET_SECURITY_KEY_FILE: path.join(tempDir, 'instance-key.json'),
    TICKET_BACKUP_DIRECTORY: path.join(tempDir, 'backups'),
    TICKET_BOOTSTRAP_ADMIN_PASSWORD: 'Supplier-E2E-Password-1',
    TICKET_BOOTSTRAP_PLATFORM_USERNAME: 'platform-e2e',
    TICKET_BOOTSTRAP_PLATFORM_PASSWORD: 'Platform-E2E-Password-2',
  },
  stdio: 'inherit',
})

let stopping = false
let databaseDropped = false
const dropDatabase = () => {
  if (databaseDropped) return
  databaseDropped = true
  const dropped = spawnSync('dropdb', [...databaseArgs, '--if-exists', '--force', databaseName], { env: process.env, stdio: 'inherit' })
  if (dropped.error) console.error(dropped.error)
}
const stop = signal => {
  if (stopping) return
  stopping = true
  child.kill(signal)
}
process.on('SIGINT', () => stop('SIGINT'))
process.on('SIGTERM', () => stop('SIGTERM'))
child.on('error', () => dropDatabase())
child.on('exit', code => {
  dropDatabase()
  process.exit(code ?? 0)
})
