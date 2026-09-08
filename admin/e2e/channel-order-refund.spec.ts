import { expect, test, type Page, type Route } from '@playwright/test'

const user = { id: 8, role: 'super_admin', scope: 'tenant', tenant_id: 1, tenant_name: '测试景区',
  capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
const account = { id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production' }
const order = { order_no: 'XHS-REFUND-TEST', external_no: 'EXTERNAL-TEST', channel: 'xiaohongshu', environment: 'production', status: 'paid', total_amount: 80,
  items: [{ product_name: '测试门票', quantity: 1, tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'unused', check_in_count: 0, pending_refund_id: 0 }] }] }
const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

async function prepare(page: Page, role = 'super_admin') {
  await page.addInitScript(value => {
    localStorage.setItem('token', 'test-token')
    localStorage.setItem('user', JSON.stringify(value))
  }, { ...user, role, permissions: role === 'viewer' ? ['channels.read', 'orders.read'] : [] })
  await page.route('**/api/v1/tenants/me', route => json(route, { ...user, id: 1, status: 'active' }))
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [account] }))
  await page.route('**/api/v1/channel-accounts/7/orders?*', route => json(route, { data: [{ ...order, ticket_count: 1, paid_cents: 8000 }], total: 1 }))
  await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, {
    order, payments: [{ method: 'xiaohongshu', status: 'paid', amount_cents: 8000 }], refunds: [], after_sales: [], check_ins: [],
  }))
  await page.goto('/channels')
  await page.getByRole('button', { name: '渠道订单', exact: true }).click()
}

test('渠道订单提供退款入口，pending 不误报到账，网络重试复用申请', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await prepare(page)
  let calls = 0
  let original: any
  await page.route('**/api/v1/payments/refunds/mixed', async route => {
    const body = route.request().postDataJSON()
    expect(body.order_no).toBe(order.order_no)
    expect(body.ticket_codes).toEqual(['DEMO-NOT-VALID'])
    expect(body.amount).toBe(80)
    expect(body.reason).toBe('游客行程改变')
    calls++
    if (calls === 1) { original = body; await route.abort('failed'); return }
    expect(body).toEqual(original)
    await json(route, { id: 9, status: 'pending', method: 'xiaohongshu' }, 201)
  })
  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await expect(dialog.getByText('¥80.00', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认申请退款' })).toBeDisabled()
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款' }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await dialog.getByRole('button', { name: '确认申请退款' }).click()
  await expect(page.getByText('退款申请已提交，等待原支付渠道确认，请刷新查看进度', { exact: true })).toBeVisible()
  await expect(page.getByText('退款已完成', { exact: true })).toHaveCount(0)
  expect(calls).toBe(2)
  expect(errors).toEqual([])
})

test('渠道订单详情也可退款，已用票在提交前说明原因', async ({ page }) => {
  await prepare(page)
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '详情', exact: true }).click()
  const detail = page.getByRole('dialog', { name: '渠道订单详情', exact: true })
  await expect(detail.getByRole('button', { name: '申请退款' })).toBeVisible()
  await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, {
    order: { ...order, items: [{ tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'active', check_in_count: 1 }] }] }, refunds: [],
  }))
  await detail.getByRole('button', { name: '申请退款' }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await expect(dialog.getByText('订单包含已使用或不可退票券，请在售后工作台核查')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认申请退款' })).toBeDisabled()
})

test('只读岗位在渠道列表及详情没有退款写入口', async ({ page }) => {
  await prepare(page, 'viewer')
  await expect(page.getByRole('button', { name: '申请退款', exact: true })).toHaveCount(0)
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '详情', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '渠道订单详情', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '申请退款', exact: true })).toHaveCount(0)
})
