import { expect, test, type Route } from '@playwright/test'

const json = (route: Route, body: unknown) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
test('线上订单默认汇总真实渠道并组合筛选、重置和刷新', async ({ page }) => {
  const user = { id: 1, role: 'super_admin', scope: 'tenant', tenant_id: 1, tenant_name: '测试景区',
    capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
  await page.addInitScript(value => { localStorage.setItem('token', 'test-token'); localStorage.setItem('user', JSON.stringify(value)) }, user)
  await page.route('**/api/v1/tenants/me', route => json(route, { ...user, status: 'active' }))
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [] }))
  await page.route('**/api/v1/devices?*', route => json(route, { data: [] }))
  const requests: Record<string, string>[] = []
  const orders = [
    { order_no: 'XHS-TEST', external_no: 'XHS-EXT', channel: 'xiaohongshu', status: 'paid', total_amount: 80, items: [] },
    { order_no: 'CTRIP-TEST', external_no: 'CTRIP-EXT', channel: 'ctrip:7', status: 'completed', total_amount: 50, items: [] },
  ]
  await page.route('**/api/v1/orders?*', async route => {
    const params = Object.fromEntries(new URL(route.request().url()).searchParams)
    requests.push(params)
    expect(params.sales_scope).toBe('online')
    const data = orders.filter(order => (!params.channel || order.channel === params.channel) && (!params.status || order.status === params.status) && (!params.search || order.external_no.includes(params.search)))
    await json(route, { data, total: data.length, channel_options: [{ value: 'xiaohongshu', label: '小红书' }, { value: 'ctrip:7', label: '携程 · 测试账号' }] })
  })
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  await page.goto('/online-order')
  await expect(page.getByRole('heading', { name: '线上订单', exact: true })).toBeVisible()
  await expect(page.getByText('XHS-TEST', { exact: true })).toBeVisible()
  await expect(page.getByText('CTRIP-TEST', { exact: true })).toBeVisible()
  expect(requests.at(-1)?.channel).toBeUndefined()
  const filters = page.locator('.filter-toolbar')
  await filters.locator('.el-select__wrapper').first().click()
  await page.getByRole('option', { name: '小红书', exact: true }).click()
  await expect(page.getByText('CTRIP-TEST', { exact: true })).toHaveCount(0)
  await filters.locator('.el-select__wrapper').last().click()
  await page.getByRole('option', { name: '已支付', exact: true }).click()
  await filters.getByPlaceholder('订单号/外部单/姓名/手机号').fill('XHS-EXT')
  await filters.getByRole('button', { name: '查询', exact: true }).click()
  await expect.poll(() => requests.at(-1)?.search).toBe('XHS-EXT')
  expect(requests.at(-1)).toMatchObject({ channel: 'xiaohongshu', status: 'paid', page: '1' })
  await page.screenshot({ path: `${process.env.TEMP}/online-orders-filtered.png`, fullPage: true })
  await filters.getByRole('button', { name: '重置', exact: true }).click()
  await expect(page.getByText('CTRIP-TEST', { exact: true })).toBeVisible()
  const count = requests.length
  await page.locator('.page-actions').getByRole('button', { name: '刷新', exact: true }).click()
  await expect.poll(() => requests.length).toBeGreaterThan(count)
  expect(requests.at(-1)).not.toHaveProperty('channel')
  expect(requests.at(-1)).not.toHaveProperty('status')
  expect(errors).toEqual([])
})
