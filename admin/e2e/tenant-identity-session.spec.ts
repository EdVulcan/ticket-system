import { expect, test, type Page } from '@playwright/test'

const identityA = { id: 1, tenant_id: 11, scope: 'tenant', role: 'admin', tenant_name: '模拟商户 A', system_code: 'MOCK-A' }
const identityB = { id: 2, tenant_id: 22, scope: 'tenant', role: 'admin', tenant_name: '模拟商户 B', system_code: 'MOCK-B' }

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}

async function prepare(page: Page) {
  await page.goto('/login')
  await page.evaluate(async identity => {
    localStorage.setItem('token', 'mock-token-a')
    localStorage.setItem('user', JSON.stringify(identity))
    const moduleURL = '/src/utils/tenantAccess.ts'
    ;(window as any).identityModule = await import(moduleURL)
    ;(window as any).identityEvents = []
    window.addEventListener('tenant-identity-refreshed', event => (window as any).identityEvents.push((event as CustomEvent).detail))
    window.addEventListener('auth-session-expired', () => (window as any).identityEvents.push('expired'))
  }, identityA)
}

for (const status of [200, 401, 'network']) test(`旧商户身份刷新 ${status} 不能覆盖或退出新登录会话`, async ({ page }) => {
  const started = deferred()
  const gate = deferred()
  await page.route('**/api/v1/tenants/me', async route => {
    started.resolve()
    await gate.promise
    if (status === 'network') { await route.abort('failed'); return }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ id: 11, name: '旧商户 A 响应', system_code: 'OLD-A' }) })
  })
  await prepare(page)
  await page.evaluate(() => { (window as any).pendingIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true) })
  await started.promise
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-token-b')
    localStorage.setItem('user', JSON.stringify(identity))
  }, identityB)
  gate.resolve()
  const returned = await page.evaluate(() => (window as any).pendingIdentity)
  const actual = await page.evaluate(() => ({ token: localStorage.getItem('token'), user: JSON.parse(localStorage.getItem('user') || '{}'), events: (window as any).identityEvents }))
  expect(actual.token).toBe('mock-token-b')
  expect(actual.user).toMatchObject(identityB)
  expect(actual.events).toEqual([])
  expect(returned).toMatchObject(identityB)
})

test('不同登录会话不共享进行中的身份刷新或两秒缓存', async ({ page }) => {
  const started = deferred()
  const gate = deferred()
  let bRequests = 0
  await page.route('**/api/v1/tenants/me', async route => {
    const oldSession = route.request().headers().authorization === 'Bearer mock-token-a'
    if (oldSession) { started.resolve(); await gate.promise }
    else bRequests += 1
    const identity = oldSession ? identityA : identityB
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ id: identity.tenant_id, name: identity.tenant_name, system_code: identity.system_code }) })
  })
  await prepare(page)
  await page.evaluate(() => { (window as any).pendingIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true) })
  await started.promise
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-token-b')
    localStorage.setItem('user', JSON.stringify(identity))
    ;(window as any).newIdentity = (window as any).identityModule.refreshStoredTenantIdentity()
  }, identityB)
  try { await expect.poll(() => bRequests, { timeout: 1500 }).toBe(1) }
  finally { gate.resolve() }
  await page.evaluate(() => Promise.all([(window as any).pendingIdentity, (window as any).newIdentity]))
  await page.evaluate(() => (window as any).identityModule.refreshStoredTenantIdentity())
  expect(bRequests).toBe(1)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}'))).toMatchObject(identityB)
})

test('新会话不会复用前一会话的成功缓存，可信身份可纠正同会话的陈旧商户资料', async ({ page }) => {
  let requests = 0
  await page.route('**/api/v1/tenants/me', async route => {
    requests += 1
    const identity = route.request().headers().authorization === 'Bearer mock-token-a' ? identityA : identityB
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ id: identity.tenant_id, name: identity.tenant_name, system_code: identity.system_code }) })
  })
  await prepare(page)
  await page.evaluate(() => (window as any).identityModule.refreshStoredTenantIdentity(true))
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-token-b')
    // Existing local merchant metadata can be stale; the trusted endpoint corrects it.
    localStorage.setItem('user', JSON.stringify({ ...identity, tenant_id: 11, tenant_name: '陈旧显示名称' }))
    return (window as any).identityModule.refreshStoredTenantIdentity()
  }, identityB)
  expect(requests).toBe(2)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}'))).toMatchObject(identityB)
})

test('旧刷新先结束不能移除新会话仍在进行的刷新', async ({ page }) => {
  const aStarted = deferred()
  const bStarted = deferred()
  const aGate = deferred()
  const bGate = deferred()
  let bRequests = 0
  await page.route('**/api/v1/tenants/me', async route => {
    const isA = route.request().headers().authorization === 'Bearer mock-token-a'
    if (isA) { aStarted.resolve(); await aGate.promise }
    else { bRequests++; bStarted.resolve(); await bGate.promise }
    const identity = isA ? identityA : identityB
    await route.fulfill({ json: { id: identity.tenant_id, name: identity.tenant_name } })
  })
  await prepare(page)
  await page.evaluate(() => { (window as any).pendingIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true) })
  await aStarted.promise
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-token-b')
    localStorage.setItem('user', JSON.stringify(identity))
    ;(window as any).newIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true)
  }, identityB)
  await bStarted.promise
  aGate.resolve()
  await page.evaluate(() => (window as any).pendingIdentity)
  await page.evaluate(() => {
    ;(window as any).duplicateIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true)
  })
  bGate.resolve()
  await page.evaluate(() => Promise.all([(window as any).newIdentity, (window as any).duplicateIdentity]))
  expect(bRequests).toBe(1)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}'))).toMatchObject(identityB)
})

test('当前身份刷新 401 仍清除登录并通知界面', async ({ page }) => {
  await page.route('**/api/v1/tenants/me', route => route.fulfill({ status: 401, json: {} }))
  await prepare(page)
  const returned = await page.evaluate(() => (window as any).identityModule.refreshStoredTenantIdentity(true))
  expect(returned).toEqual({})
  expect(await page.evaluate(() => localStorage.getItem('token'))).toBeNull()
  expect(await page.evaluate(() => localStorage.getItem('user'))).toBeNull()
  expect(await page.evaluate(() => (window as any).identityEvents)).toEqual(['expired'])
})

test('身份响应正文解析期间切换登录，也不能提交旧身份', async ({ page }) => {
  await prepare(page)
  await page.evaluate(() => {
    const originalFetch = window.fetch
    window.fetch = (input, init) => {
      if (!String(input).endsWith('/tenants/me')) return originalFetch(input, init)
      return Promise.resolve({ ok: true, status: 200, json: () => new Promise(resolve => {
        ;(window as any).finishIdentityBody = () => resolve({ id: 11, name: '旧商户 A' })
      }) } as Response)
    }
    ;(window as any).pendingIdentity = (window as any).identityModule.refreshStoredTenantIdentity(true)
  })
  await expect.poll(() => page.evaluate(() => typeof (window as any).finishIdentityBody)).toBe('function')
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-token-b')
    localStorage.setItem('user', JSON.stringify(identity))
    ;(window as any).finishIdentityBody()
  }, identityB)
  expect(await page.evaluate(() => (window as any).pendingIdentity)).toMatchObject(identityB)
  expect(await page.evaluate(() => (window as any).identityEvents)).toEqual([])
})
