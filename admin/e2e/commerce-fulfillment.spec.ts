import { expect, test, type Page } from '@playwright/test'
async function setup(page: Page, business: string, status: string, method = 'pickup', details: Record<string, unknown> = {}, overrides: Record<string, unknown> = {}) {
  const user = { id: 21, role: 'admin', scope: 'tenant', tenant_id: 21, capabilities: [], business_capabilities: [{ business_type: 'restaurant', status: 'active' }, { business_type: 'retail', status: 'active' }] }
  const row: any = { id: 42, order_no: 'FULFILL-42', business_type: business, payment_status: 'paid', refund_status: 'none', fulfillment_status: status, restaurant_fulfillment: { status, method, ...details }, retail_fulfillment: { status }, items: [], ...overrides }
  const writes: any[] = []
  const reads: string[] = []
  await page.addInitScript(u => { localStorage.setItem('token', 'test'); localStorage.setItem('user', JSON.stringify(u)) }, user)
  await page.route('**/api/v1/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname
    if (req.method() === 'GET') reads.push(path)
    let body: any = { data: [] }
    if (path.endsWith('/tenants/me')) body = { ...user, status: 'active' }
    if (path.endsWith('/commerce/orders')) body = { data: [row], total: 1 }
    if (path.endsWith('/commerce/orders/42')) body = row
    if (path.endsWith('/shipment')) body = { shipment: { id: 77 }, events: [] }
    if (['POST', 'PUT'].includes(req.method())) {
      const payload = req.postDataJSON(); writes.push({ method: req.method(), path, body: payload })
      if (path.endsWith('/restaurant-delivery')) Object.assign(row.restaurant_fulfillment, payload)
      else if (payload.status) { row.fulfillment_status = payload.status; Object.assign(row.restaurant_fulfillment, payload) }
      else if (path.endsWith('/shipments')) row.fulfillment_status = 'shipped'
      body = row
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto(`/commerce/${business}?tab=orders`)
  await expect(page.getByText('FULFILL-42', { exact: true })).toBeVisible({ timeout: 15_000 })
  return { row, writes, reads }
}
for (const [status, label, target] of [['pending_acceptance', '接单并制作', 'preparing'], ['accepted', '开始制作', 'preparing'], ['preparing', '备餐完成', 'ready']]) {
  test(`餐饮 ${status} 单次提交 ${target}`, async ({ page }) => {
    const { writes } = await setup(page, 'restaurant', status!)
    await page.getByRole('button', { name: label!, exact: true }).click()
    await expect.poll(() => writes.length).toBe(1)
    expect(writes[0].body).toEqual({ status: target })
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })
}
test('自取完成必须确认，取消不完成', async ({ page }) => {
  const { writes } = await setup(page, 'restaurant', 'ready')
  await page.getByRole('button', { name: '确认取餐', exact: true }).click()
  expect(writes).toHaveLength(0)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  expect(writes).toHaveLength(0)
  await page.getByRole('button', { name: '确认取餐', exact: true }).click()
  await page.getByRole('button', { name: '确认', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({ status: 'completed' })
})
test('配送信息提前保存，开始配送复用且不提前完成', async ({ page }) => {
  const { row, writes } = await setup(page, 'restaurant', 'preparing', 'delivery')
  await page.getByRole('button', { name: '配送信息', exact: true }).click()
  await page.getByText('跑腿平台', { exact: true }).click()
  await page.getByPlaceholder('例如：达达、闪送、顺丰同城').fill('达达')
  await page.getByPlaceholder('请填写跑腿平台返回的订单号').fill('COURIER42')
  await page.getByRole('button', { name: '保存配送信息', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: 'PUT' })
  expect(writes[0].path).toMatch(/restaurant-delivery$/)
  expect(writes[0].body).not.toHaveProperty('status')
  expect(row.fulfillment_status).toBe('preparing')
  await page.getByRole('button', { name: '出餐并配送', exact: true }).click()
  await expect.poll(() => writes.length).toBe(2)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(writes[1].body).toMatchObject({ status: 'delivering', courier_order_no: 'COURIER42' })
  expect(writes.some(w => w.body.status === 'completed')).toBe(false)
})
for (const status of ['shipped', 'in_transit', 'delivered', 'completed']) test(`电商 ${status} 展示快递信息且无需商家维护物流`, async ({ page }) => {
  const { writes, reads } = await setup(page, 'retail', status, 'pickup', {}, {
    retail_fulfillment: { status, carrier: '顺丰', tracking_no: 'SF42' },
  })
  await expect(page.getByRole('button', { name: '更新运输状态' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '标记已送达', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '填写发货信息', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('顺丰', { exact: true })).toBeVisible()
  await expect(dialog.getByText('SF42', { exact: true })).toBeVisible()
  await expect(dialog.getByText('物流进度', { exact: true })).toHaveCount(0)
  await expect(dialog.getByText('第三方物流轨迹', { exact: true })).toHaveCount(0)
  expect(reads.some(path => path.endsWith('/shipment'))).toBe(false)
  expect(writes).toHaveLength(0)
})
test('电商发货表单一次提交', async ({ page }) => {
  const { writes } = await setup(page, 'retail', 'pending_shipment')
  await page.getByRole('button', { name: '填写发货信息', exact: true }).click()
  await page.getByPlaceholder('请输入物流公司').fill('顺丰')
  await page.getByPlaceholder('请输入运单号').fill('SF42')
  await page.getByRole('button', { name: '确认发货', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({ carrier_name: '顺丰', tracking_no: 'SF42' })
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('餐饮配送完成保留实际送达确认', async ({ page }) => {
  const { writes } = await setup(page, 'restaurant', 'delivering', 'delivery')
  await page.getByRole('button', { name: '标记已送达', exact: true }).click()
  expect(writes).toHaveLength(0)
  await page.getByRole('button', { name: '确认', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({ status: 'completed' })
})

for (const provider of ['merchant', 'courier_platform']) test(`已保存 ${provider} 出餐直接配送`, async ({ page }) => {
  const details = { delivery_provider: provider, courier_platform_name: provider === 'courier_platform' ? '达达' : '', courier_order_no: provider === 'courier_platform' ? 'DD42' : '', courier_contact: '13800000000', delivery_note: '正门' }
  const { writes } = await setup(page, 'restaurant', 'preparing', 'delivery', details)
  await expect(page.getByRole('button', { name: '仅备餐完成', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '出餐并配送', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({ ...details, status: 'delivering' })
  await expect(page.getByRole('dialog')).toHaveCount(0)
})
test('仅备餐完成等待配送员，ready 仍可开始配送', async ({ page }) => {
  const { writes } = await setup(page, 'restaurant', 'preparing', 'delivery', { delivery_provider: 'merchant' })
  await page.getByRole('button', { name: '仅备餐完成', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({ status: 'ready' })
  await page.getByRole('button', { name: '开始配送', exact: true }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1].body).toMatchObject({ status: 'delivering', delivery_provider: 'merchant' })
})
for (const details of [{}, { delivery_provider: 'unknown' }, { delivery_provider: 'courier_platform', courier_platform_name: '达达' }]) test(`配送信息不完整 ${JSON.stringify(details)} 先补全`, async ({ page }) => {
  const { writes } = await setup(page, 'restaurant', 'preparing', 'delivery', details)
  await page.getByRole('button', { name: '出餐并配送', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '开始餐饮配送' })
  await expect(dialog).toBeVisible()
  expect(writes).toHaveLength(0)
  await dialog.getByRole('button', { name: '出餐并配送', exact: true }).click()
  expect(writes).toHaveLength(0)
  if (details.delivery_provider !== 'courier_platform') {
    await expect(dialog.locator('input[type=radio]:checked')).toHaveCount(0)
    await dialog.getByText('店家自送', { exact: true }).click()
  } else await dialog.getByPlaceholder('请填写跑腿平台返回的订单号').fill('DD42')
  await dialog.getByRole('button', { name: '出餐并配送', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body.status).toBe('delivering')
})
for (const overrides of [{ payment_status: 'unpaid' }, { refund_status: 'requested' }]) test(`非可履约订单无配送动作 ${JSON.stringify(overrides)}`, async ({ page }) => {
  const { writes } = await setup(page, 'restaurant', 'preparing', 'delivery', { delivery_provider: 'merchant' }, overrides)
  await expect(page.getByRole('button', { name: '出餐并配送', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '仅备餐完成', exact: true })).toHaveCount(0)
  expect(writes).toHaveLength(0)
})
