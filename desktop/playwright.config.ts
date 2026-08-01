import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4273',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'npm run dev:wails:web -- --host 127.0.0.1 --port 4273 --strictPort',
    url: 'http://127.0.0.1:4273/#/login',
    env: { VITE_API_URL: 'http://127.0.0.1:8080/api/v1' },
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
