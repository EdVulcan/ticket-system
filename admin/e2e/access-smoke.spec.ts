import { expect, test, type Page } from '@playwright/test'

const tenantUser = {
  id: 1,
  username: 'admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '示例景区',
  system_code: 'SCENIC001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
}

async function mockJSON(page: Page, pattern: string, body: unknown) {
  await page.route(pattern, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  }))
}

test('景区租户登录后只显示已授权工作区', async ({ page }) => {
  await mockJSON(page, '**/api/v1/auth/login', { token: 'tenant-token', user: tenantUser })
  await page.goto('/login')

  await page.getByPlaceholder('系统编号 (System Code)').fill('SCENIC001')
  await page.getByPlaceholder('用户名 (Username)').fill('admin')
  await page.getByPlaceholder('密码 (Password)').fill('tenant-password')
  await page.getByRole('button', { name: '登 录' }).click()

  await expect(page.getByRole('heading', { name: '经营控制台' })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toBeVisible()
  await expect(page.getByText('旅行社团队', { exact: true })).toHaveCount(0)
  await expect(page.getByText('商户开户管理', { exact: true })).toHaveCount(0)
})

test('平台身份不能进入租户业务菜单', async ({ page }) => {
  await mockJSON(page, '**/api/v1/auth/platform/login', {
    token: 'platform-token',
    user: { id: 9, username: 'platform_admin', role: 'platform_admin', scope: 'platform' },
  })
  await mockJSON(page, '**/api/v1/platform/overview', {})
  await page.goto('/login')

  await page.getByText('Platform', { exact: true }).click()
  await page.getByPlaceholder('用户名 (Username)').fill('platform_admin')
  await page.getByPlaceholder('密码 (Password)').fill('platform-password')
  await page.getByRole('button', { name: '登 录' }).click()

  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
  await expect(page.getByText('商户开户管理', { exact: true })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toHaveCount(0)

  await page.goto('/product')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
})

test('旅行社能力可以进入团队工作区', async ({ page }) => {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'travel-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, {
    ...tenantUser,
    tenant_name: '示例旅行社',
    capabilities: [{ capability: 'travel_agency', status: 'active' }],
  })
  await mockJSON(page, '**/api/v1/teams?*', { data: [], total: 0 })

  await page.goto('/teams')
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新建团队' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toHaveCount(0)
})
