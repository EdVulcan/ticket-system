import { expect, test, type Page, type Route } from '@playwright/test'

const tenantA = {
  id: 11, username: 'mock-scenic-admin', role: 'admin', scope: 'tenant', tenant_id: 11, tenant_status: 'active', is_initial_admin: true,
  tenant_name: '模拟景区 A', system_code: 'MOCK-A', permissions: ['channels.read', 'channels.write'],
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }], business_capabilities: [],
}
const tenantB = {
  id: 22, username: 'mock-retail-admin', role: 'admin', scope: 'tenant', tenant_id: 22, tenant_status: 'active', is_initial_admin: false,
  tenant_name: '模拟电商 B', system_code: 'MOCK-B', permissions: ['channels.read', 'channels.write'],
  capabilities: [], supplier_business_types: [], business_capabilities: [{ business_type: 'retail', status: 'active' }],
}
const accountA = { id: 71, code: 'xhs-sandbox-A', type: 'xiaohongshu', status: 'sandbox', environment: 'sandbox', protocol_configured: true, app_id: 'MOCK-SCENIC-A-APPID' }
const ctripA = { id: 72, code: 'ctrip-sandbox-A', type: 'ctrip', status: 'sandbox', environment: 'sandbox', protocol_configured: true, app_id: 'MOCK-CTRIP-A' }
const accountB = { id: 81, code: 'wechat-retail-B', type: 'wechat_miniapp', status: 'active', environment: 'production', protocol_configured: true, app_id: 'MOCK-RETAIL-B-APPID' }
const json = (route: Route, body: unknown) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
const row = (page: Page, code: string) => page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: code })
const pageErrors = new WeakMap<Page, string[]>()
test.beforeEach(async ({ page }) => {
  const errors: string[] = []
  pageErrors.set(page, errors)
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => {
    sessionStorage.setItem('channel-toast-history', '[]')
    new MutationObserver(mutations => {
      for (const mutation of mutations) for (const node of mutation.addedNodes) {
        const element = node instanceof Element ? node : node.parentElement
        const messages = [element?.closest('.el-message'), ...Array.from(element?.querySelectorAll('.el-message') || [])]
        for (const message of messages) if (message?.textContent?.trim()) {
          const history = JSON.parse(sessionStorage.getItem('channel-toast-history') || '[]')
          history.push(message.textContent.trim())
          sessionStorage.setItem('channel-toast-history', JSON.stringify(history))
        }
      }
    }).observe(document, { childList: true, subtree: true })
  })
})
test.afterEach(async ({ page }) => {
  await settleBrowser(page)
  expect(pageErrors.get(page)).toEqual([])
  expect(await page.evaluate(() => JSON.parse(sessionStorage.getItem('channel-toast-history') || '[]'))).toEqual([])
})

function deferred() {
  let release!: () => void
  const promise = new Promise<void>(resolve => { release = resolve })
  return { promise, release }
}

async function settleBrowser(page: Page) {
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

async function setup(page: Page, options: { initialAGate?: Promise<void>; handler?: (route: Route, path: string) => Promise<boolean> } = {}) {
  const reads: { tenant: 'A' | 'B'; path: string }[] = []
  const writes: string[] = []
  await page.addInitScript(identity => {
    localStorage.setItem('token', 'mock-tenant-a-token')
    localStorage.setItem('user', JSON.stringify(identity))
  }, tenantA)
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^.*\/api\/v1/, '')
    const tenant = request.headers().authorization === 'Bearer mock-tenant-a-token' ? 'A' : 'B'
    if (request.method() !== 'GET') writes.push(path)
    if (await options.handler?.(route, path)) return
    if (path === '/tenants/me') {
      const identity = tenant === 'A' ? tenantA : tenantB
      return json(route, { ...identity, id: identity.tenant_id, name: identity.tenant_name, status: 'active' })
    }
    if (path === '/channel-accounts') {
      reads.push({ tenant, path })
      if (tenant === 'A' && options.initialAGate) await options.initialAGate
      return json(route, { data: tenant === 'A' ? [accountA, ctripA] : [accountB] })
    }
    return json(route, { data: [], total: 0 })
  })
  const identityResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/tenants/me'))
  await page.goto('/channels')
  await (await identityResponse).finished()
  await settleBrowser(page)
  await expect(page.getByRole('heading', { name: '渠道连接', exact: true })).toBeVisible()
  return { reads, writes }
}

async function switchToB(page: Page) {
  await page.evaluate(identity => {
    localStorage.setItem('token', 'mock-tenant-b-token')
    localStorage.setItem('user', JSON.stringify(identity))
    window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: identity }))
  }, tenantB)
  await settleBrowser(page)
}

async function openAParameters(page: Page) {
  await row(page, accountA.code).getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '小红书参数', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '小红书小程序参数', exact: true })
  await expect(dialog.getByRole('textbox', { name: '小程序 AppID', exact: true })).toHaveValue(accountA.app_id)
  return dialog
}

test('切换租户清除原账号和参数窗，自动加载当前租户渠道', async ({ page }, testInfo) => {
  const { reads, writes } = await setup(page)
  await expect(row(page, accountA.code)).toBeVisible()
  const dialog = await openAParameters(page)
  await switchToB(page)
  await expect(dialog).toHaveCount(0)
  await expect(row(page, accountB.code)).toBeVisible()
  const evidence = {
    storedTenantID: await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}').tenant_id),
    oldXhsRows: await row(page, accountA.code).count(),
    oldCtripRows: await row(page, ctripA.code).count(),
    newWechatRows: await row(page, accountB.code).count(),
    parameterDialogVisible: await dialog.isVisible(),
    reads, writes,
  }
  await testInfo.attach('identity-switch-evidence.json', { body: Buffer.from(JSON.stringify(evidence, null, 2)), contentType: 'application/json' })
  await page.screenshot({ path: testInfo.outputPath('after-fix-tenant-b-channels.png'), animations: 'disabled' })
  expect.soft(evidence.oldXhsRows).toBe(0)
  expect.soft(evidence.oldCtripRows).toBe(0)
  expect.soft(evidence.parameterDialogVisible).toBe(false)
})

test('当前账号加载成功后原租户迟到列表不能覆盖', async ({ page }, testInfo) => {
  const gate = deferred()
  const { reads, writes } = await setup(page, { initialAGate: gate.promise })
  await expect.poll(() => reads.filter(read => read.tenant === 'A').length).toBe(1)
  await switchToB(page)
  await expect(row(page, accountB.code)).toBeVisible()
  const response = page.waitForResponse(res => res.request().headers().authorization === 'Bearer mock-tenant-a-token' && new URL(res.url()).pathname.endsWith('/channel-accounts'))
  gate.release()
  await (await response).finished()
  await settleBrowser(page)
  const evidence = {
    storedTenantID: await page.evaluate(() => JSON.parse(localStorage.getItem('user') || '{}').tenant_id),
    oldXhsRows: await row(page, accountA.code).count(),
    oldCtripRows: await row(page, ctripA.code).count(),
    newWechatRows: await row(page, accountB.code).count(),
    reads, writes,
  }
  await testInfo.attach('late-list-evidence.json', { body: Buffer.from(JSON.stringify(evidence, null, 2)), contentType: 'application/json' })
  await page.screenshot({ path: testInfo.outputPath('after-fix-ignores-late-a-list.png'), animations: 'disabled' })
  expect.soft(evidence.oldXhsRows).toBe(0)
  expect.soft(evidence.newWechatRows).toBe(1)
})

test('同一身份的普通刷新保留未保存参数，不重新加载账号', async ({ page }) => {
  const { reads, writes } = await setup(page)
  await expect(row(page, accountA.code)).toBeVisible()
  const dialog = await openAParameters(page)
  await dialog.getByRole('textbox', { name: '小程序 AppID', exact: true }).fill('MOCK-UNSAVED-APPID')
  const readsBefore = reads.length
  await page.evaluate(identity => {
    localStorage.setItem('user', JSON.stringify(identity))
    window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: identity }))
  }, { ...tenantA, tenant_name: '模拟景区 A 展示名更新', permissions: [...tenantA.permissions].reverse() })
  await settleBrowser(page)
  await expect(dialog.getByRole('textbox', { name: '小程序 AppID', exact: true })).toHaveValue('MOCK-UNSAVED-APPID')
  expect(reads.length).toBe(readsBefore)
  expect(writes).toEqual([])
})

async function switchOnClick(page: Page, button: ReturnType<Page['getByRole']>) {
  await button.evaluate((element, identity) => {
    element.addEventListener('click', () => queueMicrotask(() => {
      localStorage.setItem('token', 'mock-tenant-b-token')
      localStorage.setItem('user', JSON.stringify(identity))
      window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: identity }))
    }), { capture: true, once: true })
  }, tenantB)
  await button.click()
  await settleBrowser(page)
}

test('确认切换环境的瞬间切换租户，不发送原账号写入', async ({ page }) => {
  const { writes } = await setup(page)
  await row(page, accountA.code).getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '切换正式环境', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: '切换运行环境', exact: true })
  await expect(confirmation).toBeVisible()
  await switchOnClick(page, confirmation.getByRole('button', { name: '确定', exact: true }))
  await expect(confirmation).toHaveCount(0)
  await expect(row(page, accountB.code)).toBeVisible()
  expect(writes).toEqual([])
})

const mappingA = { id: 91, product_id: 101, external_code: 'MOCK-A-PRODUCT', display_name: '模拟票 A', channel_sale_cents: 1, status: 'active' }
const mappingConfig = { external_sku_id: 'MOCK-A-SKU', category_id: 'travel', poi_ids: ['poi-a'], image_url: '/mock-image.png', description: '模拟商品说明', product_path: '/pages/product', order_path: '/pages/order', product_type: 1, settle_type: 1, sync_status: 'pending', audit_status: 'pending' }
test('多步骤发布保存途中切换租户，停止后续配置和平台同步', async ({ page }) => {
  const gate = deferred()
  const { writes } = await setup(page, { handler: async (route, path) => {
    if (path === '/products') { await json(route, { data: [{ id: 101, name: '模拟景区票', type: 'online', product_kind: 'ticket' }] }); return true }
    if (path === '/channel-accounts/mappings') { await json(route, { data: [mappingA] }); return true }
    if (path.endsWith('/xiaohongshu-categories')) { await json(route, { data: [{ category_id: 'travel', name: '旅游', support_trade: true }] }); return true }
    if (path.endsWith('/xiaohongshu-pois')) { await json(route, { data: [{ poi_id: 'poi-a', name: '模拟门店 A' }] }); return true }
    if (path.endsWith('/xiaohongshu-product')) { await json(route, mappingConfig); return true }
    if (path === '/channel-accounts/71/mappings/91') { await gate.promise; await json(route, mappingA); return true }
    return false
  } })
  await row(page, accountA.code).getByRole('button', { name: '商品映射', exact: true }).click()
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '小红书商品发布配置', exact: true })
  await expect(dialog.getByRole('textbox', { name: '商品说明', exact: true })).toHaveValue(mappingConfig.description)
  await dialog.getByRole('button', { name: '保存并同步', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  await switchToB(page)
  const response = page.waitForResponse(res => res.request().method() === 'PATCH' && new URL(res.url()).pathname.endsWith('/channel-accounts/71/mappings/91'))
  gate.release()
  await (await response).finished()
  await expect(row(page, accountB.code)).toBeVisible()
  expect(writes).toEqual(['/channel-accounts/71/mappings/91'])
  await expect(page.locator('.el-message')).toHaveCount(0)
})

test('旧参数保存迟到响应不影响新租户参数草稿或显示成功提示', async ({ page }) => {
  const gate = deferred()
  const { writes } = await setup(page, { handler: async (route, path) => {
    if (path === '/channel-accounts/71/xiaohongshu-config') { await gate.promise; await json(route, {}); return true }
    return false
  } })
  const dialog = await openAParameters(page)
  await dialog.getByLabel('小程序 AppSecret', { exact: true }).fill('MOCK-ONLY-SECRET')
  await dialog.getByLabel('Token（令牌）', { exact: true }).fill('MockTokenA')
  await dialog.getByLabel('EncodingAESKey（消息加密密钥）', { exact: true }).fill('A'.repeat(43))
  await dialog.getByRole('button', { name: '保存参数', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  await switchToB(page)
  await row(page, accountB.code).getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '微信小程序参数', exact: true }).click()
  const newDialog = page.getByRole('dialog', { name: '微信小程序参数', exact: true })
  await newDialog.getByLabel('小程序 AppID', { exact: true }).fill('MOCK-UNSAVED-B-APPID')
  const response = page.waitForResponse(res => new URL(res.url()).pathname.endsWith('/channel-accounts/71/xiaohongshu-config'))
  gate.release()
  await (await response).finished()
  await expect(newDialog.getByLabel('小程序 AppID', { exact: true })).toHaveValue('MOCK-UNSAVED-B-APPID')
  await expect(page.locator('.el-message')).toHaveCount(0)
})

test('商城图片子弹窗保存途中切换租户，旧响应静默结束', async ({ page }) => {
  const gate = deferred()
  const { writes } = await setup(page, { handler: async (route, path) => {
    if (path === '/channel-accounts/71/storefront') {
      if (route.request().method() === 'PUT') await gate.promise
      await json(route, { image_url: '' }); return true
    }
    return false
  } })
  await row(page, accountA.code).getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '商城图片', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商城图片', exact: true })
  const save = dialog.getByRole('button', { name: '保存', exact: true })
  await expect(save).toBeEnabled()
  await save.click()
  await expect.poll(() => writes.length).toBe(1)
  await switchToB(page)
  const response = page.waitForResponse(res => res.request().method() === 'PUT' && new URL(res.url()).pathname.endsWith('/channel-accounts/71/storefront'))
  gate.release()
  await (await response).finished()
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('.el-message')).toHaveCount(0)
})

const refundOrder = { order_no: 'MOCK-REFUND-A', tenant_id: 11, channel: 'xiaohongshu', environment: 'production', status: 'paid', total_amount: 1,
  items: [{ product_name: '模拟退款票', quantity: 1, refund_type: 'free', tickets: [{ ticket_code: 'MOCK-NOT-A-VALID-TICKET', status: 'unused', check_in_count: 0 }] }] }
async function prepareRefund(page: Page, options: { detailGate?: Promise<void>; submitGate?: Promise<void>; exception?: boolean } = {}) {
  const order = options.exception ? { ...refundOrder, items: refundOrder.items.map(item => ({ ...item, refund_type: 'no_refund' })) } : refundOrder
  const state = await setup(page, { handler: async (route, path) => {
    if (path === '/orders') { await json(route, { data: [order], total: 1, channel_options: [] }); return true }
    if (path === `/orders/${order.order_no}`) { if (options.detailGate) await options.detailGate; await json(route, { order, refunds: [] }); return true }
    if (path === '/payments/refunds/mixed') { if (options.submitGate) await options.submitGate; await json(route, { status: 'pending' }); return true }
    return false
  } })
  await page.goto('/online-order')
  await row(page, order.order_no).getByRole('button', { name: '退款', exact: true }).click()
  return { ...state, dialog: page.getByRole('dialog', { name: '申请原路退款', exact: true }) }
}

test('共享退款弹窗的原租户详情迟到时保持关闭', async ({ page }) => {
  const gate = deferred()
  const { dialog, writes } = await prepareRefund(page, { detailGate: gate.promise })
  await expect(dialog).toBeVisible()
  await switchToB(page)
  const response = page.waitForResponse(res => new URL(res.url()).pathname.endsWith(`/orders/${refundOrder.order_no}`))
  gate.release()
  await (await response).finished()
  await expect(dialog).toHaveCount(0)
  expect(writes).toEqual([])
  await expect(page.locator('.el-message')).toHaveCount(0)
})

test('共享退款提交迟到时不提示成功或继续查询旧订单', async ({ page }) => {
  const gate = deferred()
  const { dialog, writes } = await prepareRefund(page, { submitGate: gate.promise })
  await expect(dialog.getByText('¥1.00', { exact: true })).toBeVisible()
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('模拟取消行程')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  await switchToB(page)
  const response = page.waitForResponse(res => new URL(res.url()).pathname.endsWith('/payments/refunds/mixed'))
  gate.release()
  await (await response).finished()
  await expect(dialog).toHaveCount(0)
  expect(writes).toEqual(['/payments/refunds/mixed'])
  await expect(page.locator('.el-message')).toHaveCount(0)
})

test('共享退款例外确认瞬间切换租户，不发送退款申请', async ({ page }) => {
  const { dialog, writes } = await prepareRefund(page, { exception: true })
  await expect(dialog.getByText('¥1.00', { exact: true })).toBeVisible()
  await dialog.getByPlaceholder('请填写本次例外退款的具体原因').fill('模拟政策纠错')
  await dialog.getByText('以初始管理员身份申请例外退款', { exact: true }).click()
  await expect(dialog.getByRole('checkbox', { name: '以初始管理员身份申请例外退款', exact: true })).toBeChecked()
  await dialog.getByRole('button', { name: '确认例外退款', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: '确认管理员例外退款', exact: true })
  await switchOnClick(page, confirmation.getByRole('button', { name: '确认提交例外退款', exact: true }))
  await expect(confirmation).toHaveCount(0)
  await expect(dialog).toHaveCount(0)
  expect(writes).toEqual([])
})
