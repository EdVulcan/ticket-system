import { expect, test, type Page, type Route } from '@playwright/test'

const supplierUser = {
  id: 8,
  username: 'supplier_admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '青云景区',
  system_code: 'QY001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareSupplier(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, supplierUser)
  await page.route('**/api/v1/teams?page=*', route => json(route, { data: [], total: 0 }))
  await page.route('**/api/v1/teams/contracts', async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      expect(body.travel_tenant_id).toBe(21)
      expect(body.price_rules).toEqual([{ product_id: 101, price_cents: 8800, max_quantity: 50 }])
      await json(route, { id: 31, ...body }, 201)
      return
    }
    await json(route, { data: [] })
  })
  await page.route('**/api/v1/teams/contract-partners', route => json(route, { data: [
    { tenant_id: 21, name: '山河旅行社', system_code: 'SHLX', relationship_id: 6 },
  ] }))
  await page.route('**/api/v1/products?*', route => json(route, { data: [
    { id: 101, name: '青云景区团队票', status: 'online', is_distributable: true },
  ] }))
}

test('供应商按旅行社和产品名称维护合同结算价', async ({ page }) => {
  await prepareSupplier(page)
  await page.goto('/teams')
  await page.getByRole('tab', { name: '旅行社合同' }).click()
  await page.getByRole('button', { name: '新增旅行社合同' }).click()

  const dialog = page.getByRole('dialog', { name: '新增旅行社合同' })
  await dialog.getByRole('combobox', { name: /旅行社/ }).click()
  await page.getByRole('option', { name: /山河旅行社/ }).click()
  await dialog.getByRole('textbox', { name: /合同号/ }).fill('QY-TEAM-2026')
  await dialog.getByRole('combobox', { name: '产品' }).click()
  await page.getByRole('option', { name: '青云景区团队票' }).click()
  await dialog.getByRole('spinbutton', { name: '结算价（元）' }).fill('88')
  await dialog.getByRole('spinbutton', { name: '每单上限（0不限）' }).fill('50')
  await expect(dialog.getByText('保存后同时作为该旅行社的供货结算价')).toBeVisible()
  await dialog.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('旅行社合同已创建')).toBeVisible()
})
