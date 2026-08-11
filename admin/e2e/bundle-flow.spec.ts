import { expect, test, type Page } from '@playwright/test'

const distributorUser = {
  id: 12,
  username: 'distributor_admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 20,
  tenant_name: '示例分销商',
  system_code: 'DIST001',
  capabilities: [{ capability: 'distributor', status: 'active' }],
}

async function json(route: import('@playwright/test').Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareDistributor(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'distributor-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, distributorUser)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: distributorUser.tenant_id,
    name: distributorUser.tenant_name,
    system_code: distributorUser.system_code,
    status: 'active',
    capabilities: distributorUser.capabilities,
    supplier_business_types: [],
  }))
  await page.route('**/api/v1/distribution/suppliers', route => json(route, { data: [] }))
  await page.route('**/api/v1/distribution/bundle-components?*', route => json(route, { data: [
    { seller_product_id: 101, seller_product_name: '东园成人票', supplier_name: '东园景区' },
    { seller_product_id: 202, seller_product_name: '西园观光票', supplier_name: '西园景区' },
  ] }))
  await page.route('**/api/v1/distribution/bundles', route => json(route, { data: [{
    id: 31, name: '双园联票', type: 'offline', retail_price_cents: 15000, status: 'online', version: 1, available: true,
    components: [
      { id: 1, seller_product_id: 101, seller_product_name: '东园成人票', supplier_name: '东园景区', quantity: 1, retail_allocation_cents: 8000 },
      { id: 2, seller_product_id: 202, seller_product_name: '西园观光票', supplier_name: '西园景区', quantity: 1, retail_allocation_cents: 7000 },
    ],
  }] }))
}

test('分销商可查看组合责任明细并进入版本编辑', async ({ page }) => {
  await prepareDistributor(page)
  await page.goto('/distribution')
  await page.getByRole('tab', { name: '组合产品' }).click()

  const row = page.getByRole('row').filter({ hasText: '双园联票' })
  await expect(row).toContainText('¥150.00')
  await expect(row).toContainText('东园景区 · 东园成人票 × 1')
  await expect(row).toContainText('西园景区 · 西园观光票 × 1')
  await expect(row.getByText('销售中', { exact: true })).toBeVisible()

  await row.getByRole('button', { name: '编辑' }).click()
  const dialog = page.getByRole('dialog', { name: '编辑组合产品' })
  await expect(dialog.locator('input').first()).toHaveValue('双园联票')
  await expect(dialog.getByText('已分摊 ¥150.00，金额一致')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '保存并下架' })).toBeVisible()
})
