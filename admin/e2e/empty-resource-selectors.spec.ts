import { expect, test, type Page, type Route } from '@playwright/test'

const users = {
  supplier: { id: 1, username: 'supplier_admin', role: 'super_admin', scope: 'tenant', tenant_id: 1, tenant_name: '测试景区', system_code: 'TEST001', capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] },
  distributor: { id: 2, username: 'distributor_admin', role: 'super_admin', scope: 'tenant', tenant_id: 2, tenant_name: '测试分销商', system_code: 'DIST001', capabilities: [{ capability: 'distributor', status: 'active' }] },
  travel: { id: 3, username: 'travel_admin', role: 'super_admin', scope: 'tenant', tenant_id: 3, tenant_name: '测试旅行社', system_code: 'TRAVEL001', capabilities: [{ capability: 'travel_agency', status: 'active' }] },
}

const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

async function loginAs(page: Page, user: typeof users.supplier) {
  await page.addInitScript(current => {
    localStorage.setItem('token', 'test-token')
    localStorage.setItem('user', JSON.stringify(current))
  }, user)
}

async function expectBlankSelect(select: ReturnType<Page['getByRole']>) {
  await expect(select).toHaveValue('')
  await expect(select.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]')).not.toContainText('0')
}

test('新增检票点未选择景区时不显示 0', async ({ page }) => {
  await loginAs(page, users.supplier)
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [], total: 0 }))
  await page.route('**/api/v1/scenic-areas', route => json(route, { data: [
    { id: 11, name: '东园', status: 'active' }, { id: 12, name: '西园', status: 'active' },
  ] }))
  await page.goto('/checkpoint')
  await page.getByRole('button', { name: '新增检票点' }).click()
  await expectBlankSelect(page.getByRole('dialog', { name: '新增检票点' }).getByRole('combobox', { name: '所属景区' }))
})

test('新建组合产品的组件选择器不显示 0', async ({ page }) => {
  await loginAs(page, users.distributor)
  await page.route('**/api/v1/distribution/suppliers', route => json(route, { data: [] }))
  await page.route('**/api/v1/distribution/bundles', route => json(route, { data: [] }))
  await page.route('**/api/v1/distribution/bundle-components?*', route => json(route, { data: [] }))
  await page.goto('/distribution')
  await page.getByRole('tab', { name: '组合产品' }).click()
  await page.getByRole('button', { name: '新建组合产品' }).click()
  const selects = page.getByRole('dialog', { name: '新建组合产品' }).getByRole('combobox')
  await expect(selects).toHaveCount(2)
  await expectBlankSelect(selects.nth(0))
  await expectBlankSelect(selects.nth(1))
})

test('旅行社新建团队的合同和景区选择器不显示 0', async ({ page }) => {
  await loginAs(page, users.travel)
  await page.route('**/api/v1/teams?page=*', route => json(route, { data: [], total: 0 }))
  await page.route('**/api/v1/teams/contracts', route => json(route, { data: [] }))
  await page.route('**/api/v1/teams/agents', route => json(route, { data: [] }))
  await page.route('**/api/v1/teams/guides', route => json(route, { data: [] }))
  await page.route('**/api/v1/teams/vehicles', route => json(route, { data: [] }))
  await page.goto('/teams')
  await page.getByRole('button', { name: '新建团队' }).click()
  const dialog = page.getByRole('dialog', { name: '新建团队' })
  await expectBlankSelect(dialog.getByRole('combobox', { name: '合作合同' }))
  await expectBlankSelect(dialog.getByRole('combobox', { name: '入园景区' }))
})
