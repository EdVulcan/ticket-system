import { expect, test, type Page, type Route } from '@playwright/test'

const user = {
  id: 8,
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '测试景区',
  permissions: ['orders.read', 'channels.read'],
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}

const account = { id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production' }
const orderNo = 'AUTO-REFUND-ORDER'
const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

function order(refundPending: boolean) {
  return {
    order_no: orderNo,
    external_no: 'AUTO-REFUND-EXTERNAL',
    channel: 'xiaohongshu',
    environment: 'production',
    status: refundPending ? 'paid' : 'refunded',
    total_amount: 80,
    contact_name: '自动刷新游客',
    contact_phone: '13800138000',
    created_at: '2026-09-09T10:00:00Z',
    items: [{ id: 1, product_name: '自动退款测试票', quantity: 1, tickets: [{ ticket_code: 'AUTO-REFUND-TICKET', status: refundPending ? 'unused' : 'refunded', check_in_count: 0, pending_refund_id: refundPending ? 9 : 0 }] }],
  }
}

async function prepare(page: Page, entry: 'online' | 'channel') {
  let completed = false
  const requests: string[] = []
  await page.addInitScript(value => {
    localStorage.setItem('token', 'test-token')
    localStorage.setItem('user', JSON.stringify(value))
  }, user)
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    requests.push(`${request.method()} ${path}`)
    const currentOrder = order(!completed)
    if (path === '/tenants/me') return json(route, { ...user, status: 'active' })
    if (path === '/orders') return json(route, { data: [currentOrder], total: 1, channel_options: [{ value: 'xiaohongshu', label: '小红书' }] })
    if (path === `/orders/${orderNo}`) return json(route, { order: currentOrder, fulfillments: [] })
    if (path === '/checkpoints' || path === '/devices') return json(route, { data: [] })
    if (path === '/channel-accounts') return json(route, { data: [account] })
    if (path === '/channel-accounts/7/orders') return json(route, { data: [{ ...currentOrder, refund_pending: !completed, ticket_count: 1, used_ticket_count: 0, refunded_ticket_count: completed ? 1 : 0, paid_cents: 8000, refunded_cents: completed ? 8000 : 0 }], total: 1 })
    if (path === `/channel-accounts/7/orders/${orderNo}`) return json(route, { order: currentOrder, payments: [{ method: 'xiaohongshu', status: completed ? 'refunded' : 'paid', amount_cents: 8000 }], refunds: completed ? [{ refund_no: 'R-AUTO', method: 'xiaohongshu', status: 'succeeded', amount_cents: 8000 }] : [{ refund_no: 'R-AUTO', method: 'xiaohongshu', status: 'pending', amount_cents: 8000 }], after_sales: [], check_ins: [] })
    return json(route, { data: [] })
  })

  if (entry === 'online') {
    await page.goto('/online-order')
    await page.getByPlaceholder('订单号/外部单/姓名/手机号').fill(orderNo)
    await page.getByPlaceholder('订单号/外部单/姓名/手机号').press('Enter')
    await page.getByRole('button', { name: '详情', exact: true }).click()
    await expect(page.getByRole('dialog', { name: '订单详情' })).toBeVisible()
  } else {
    await page.goto('/channels')
    await page.getByRole('button', { name: '渠道订单', exact: true }).click()
    const ordersDialog = page.getByRole('dialog', { name: /渠道订单：/ })
    await ordersDialog.getByPlaceholder('订单号、外部单号、姓名或手机号').fill(orderNo)
    await ordersDialog.getByPlaceholder('订单号、外部单号、姓名或手机号').press('Enter')
    await page.getByRole('button', { name: '详情', exact: true }).click()
    await expect(page.getByRole('dialog', { name: '渠道订单详情' })).toBeVisible()
  }
  await expect(page.getByText('退款处理中', { exact: true }).first()).toBeVisible()
  completed = true
  return requests
}

for (const entry of ['online', 'channel'] as const) {
  test(`${entry} pending refund refreshes list and open detail without writes`, async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
    const requests = await prepare(page, entry)
    await expect.poll(() => page.getByText('已退款', { exact: true }).count(), { timeout: 8_000 }).toBeGreaterThan(0)
    const detailDialog = page.getByRole('dialog', { name: entry === 'online' ? '订单详情' : '渠道订单详情' })
    await expect(detailDialog).toBeVisible()
    await expect(detailDialog.getByRole('cell', { name: '已退款', exact: true }).first()).toBeVisible()
    if (entry === 'online') await expect(page.getByPlaceholder('订单号/外部单/姓名/手机号')).toHaveValue(orderNo)
    else await expect(page.getByRole('dialog', { name: /渠道订单：/ }).getByPlaceholder('订单号、外部单号、姓名或手机号')).toHaveValue(orderNo)
    expect(requests.filter(request => !request.startsWith('GET '))).toEqual([])
    await expect(page).toHaveURL(entry === 'online' ? /\/online-order$/ : /\/channels$/)
    await expect(page).toHaveTitle('景区票务管理系统')
    await expect(page.locator('vite-error-overlay')).toHaveCount(0)
    await page.screenshot({ path: `${process.env.TEMP || '/tmp'}/refund-auto-${entry}.png`, animations: 'disabled' })
    expect(errors).toEqual([])
    const completedRequestCount = requests.length
    await page.waitForTimeout(2_700)
    expect(requests).toHaveLength(completedRequestCount)
  })
}

test('returning to the page during a pending refresh resumes polling', async ({ page }) => {
  await prepare(page, 'online')
  let reads = 0
  let release: (() => void) | undefined
  const heldResponse = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/orders?*', async route => {
    reads += 1
    if (reads === 1) await heldResponse
    await json(route, { data: [order(true)], total: 1 })
  })
  await page.route(`**/api/v1/orders/${orderNo}`, route => json(route, { order: order(true), fulfillments: [] }))
  await expect.poll(() => reads, { timeout: 6000 }).toBe(1)
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  release!()
  await expect.poll(() => reads, { timeout: 6000 }).toBeGreaterThan(1)
  await page.goto('/login')
  const stoppedAt = reads
  await page.waitForTimeout(2800)
  expect(reads).toBe(stoppedAt)
})
