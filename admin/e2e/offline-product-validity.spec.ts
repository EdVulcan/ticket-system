import { expect, test } from '@playwright/test'

test('editing only refund policy preserves a rolling 30-day ticket validity', async ({ page }) => {
  const user = { id: 1, role: 'super_admin', scope: 'tenant', tenant_id: 1,
    capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
  await page.addInitScript(value => { localStorage.setItem('token', 'test-token'); localStorage.setItem('user', JSON.stringify(value)) }, user)
  const product = { id: 3, tenant_id: 1, name: '有效期回归票', price: 80, settlement_price: 0, type: 'offline', status: 'online', scenic_area_id: 2,
    refund_type: 'no_refund', validity_type: 'days', validity_days: 30, stock_type: 'unlimited', tags: '[]',
    rule: { name: '有效期回归票', validity_type: 'date', groups: [{ group_name: '大门', max_total_check_in: 1, items: [{ check_point_id: 2, max_per_check_in: 1 }] }] } }
  await page.route('**/api/v1/tenants/me', r => r.fulfill({ json: { ...user, status: 'active' } }))
  await page.route('**/api/v1/scenic-areas', r => r.fulfill({ json: { data: [{ id: 2, name: '测试景区', status: 'active' }] } }))
  await page.route('**/api/v1/checkpoints?*', r => r.fulfill({ json: { data: [{ id: 2, name: '大门', scenic_area_id: 2 }] } }))
  await page.route('**/api/v1/products?*', r => r.fulfill({ json: { data: [product], total: 1 } }))
  let submitted: any
  await page.route('**/api/v1/products/3', async r => { submitted = r.request().postDataJSON(); await r.fulfill({ json: { message: 'updated successfully' } }) })
  await page.goto('/product/offline')
  await page.getByRole('row').filter({ hasText: '有效期回归票' }).getByRole('button', { name: '编辑', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByText('允许退票', { exact: true }).click()
  await dialog.getByRole('button', { name: '保存并发布', exact: true }).click()
  await expect.poll(() => submitted?.product?.refund_type).toBe('free')
  expect(submitted.product.validity_days).toBe(30)
  expect(submitted.product.validity_type).toBe('days')
})
