import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/tenants/me', route => route.fulfill({ status: 503, json: {} }))
})

for (const failure of ['401', '500', 'network']) test(`旧会话的 ${failure} 响应不能影响新登录`, async ({ page }) => {
  let release!: () => void
  let started!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  const requestStarted = new Promise<void>(resolve => { started = resolve })
  await page.route('**/api/v1/session-regression', async route => {
    started()
    await gate
    if (failure === 'network') await route.abort('failed')
    else await route.fulfill({ status: Number(failure), json: { error: '旧账号请求失败' } })
  })
  await page.goto('/login')
  await page.evaluate(async () => {
    localStorage.setItem('token', 'mock-old-token')
    localStorage.setItem('user', JSON.stringify({ id: 1, scope: 'tenant', tenant_id: 11 }))
    const moduleURL = '/src/utils/request.ts'
    const { default: request } = await import(moduleURL)
    ;(window as any).pendingRequest = request.get('/session-regression').catch(() => null)
  })
  await requestStarted
  await page.evaluate(() => {
    localStorage.setItem('token', 'mock-new-token')
    localStorage.setItem('user', JSON.stringify({ id: 2, scope: 'tenant', tenant_id: 22 }))
  })
  release()
  await page.evaluate(() => (window as any).pendingRequest)
  expect(await page.evaluate(() => localStorage.getItem('token'))).toBe('mock-new-token')
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}').tenant_id)).toBe(22)
  // Check immediately: waiting for count=0 could pass after an unwanted toast expires.
  expect(await page.locator('.el-message').allTextContents()).toEqual([])
})

test('当前会话 401 仍清除失效登录', async ({ page }) => {
  await page.route('**/api/v1/session-regression', route => route.fulfill({ status: 401, json: { error: 'unauthorized' } }))
  await page.goto('/login')
  await page.evaluate(async () => {
    localStorage.setItem('token', 'mock-current-token')
    localStorage.setItem('user', JSON.stringify({ id: 1, scope: 'tenant', tenant_id: 11 }))
    const moduleURL = '/src/utils/request.ts'
    const { default: request } = await import(moduleURL)
    await request.get('/session-regression').catch(() => null)
  })
  expect(await page.evaluate(() => localStorage.getItem('token'))).toBeNull()
  expect(await page.evaluate(() => localStorage.getItem('user'))).toBeNull()
})
