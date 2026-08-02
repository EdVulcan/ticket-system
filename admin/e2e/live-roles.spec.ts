import { expect, test } from '@playwright/test'

async function tenantLogin(page: import('@playwright/test').Page, systemCode: string, password: string) {
  await page.goto('/login')
  await page.getByPlaceholder('商户系统编号').fill(systemCode)
  await page.getByPlaceholder('用户名').fill('admin')
  await page.getByPlaceholder('密码').fill(password)
  await page.getByRole('button', { name: '登 录' }).click()
  await expect(page.getByRole('heading', { name: '经营控制台' })).toBeVisible()
}

test('真实平台账号登录并读取治理总览', async ({ page }) => {
  await page.goto('/login')
  await page.getByText('平台登录', { exact: true }).click()
  await page.getByPlaceholder('用户名').fill('platform-e2e')
  await page.getByPlaceholder('密码').fill('Platform-E2E-Password-2')
  await page.getByRole('button', { name: '登 录' }).click()

  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
  await expect(page.getByText('租户总数').locator('..').getByText('3', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '租户治理' }).click()
  await expect(page.getByRole('heading', { name: '商户开户管理' })).toBeVisible()
  await expect(page.getByText('E2E 分销商', { exact: true })).toBeVisible()
  await expect(page.getByText('E2E 旅行社', { exact: true })).toBeVisible()
})

test('真实景区供应商只能进入供应商工作区', async ({ page }) => {
  await tenantLogin(page, 'SYS001', 'Supplier-E2E-Password-1')
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '分销商管理' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await page.getByRole('menuitem', { name: '旅行社团队' }).click()
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
})

test('真实分销商只能进入销售与分销工作区', async ({ page }) => {
  await tenantLogin(page, 'E2EDIST', 'Distributor-E2E-Password-3')
  await expect(page.getByRole('menuitem', { name: '分销商管理' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toHaveCount(0)
  await page.getByRole('menuitem', { name: '分销商管理' }).click()
  await expect(page).toHaveURL(/\/distribution$/)
  await expect(page.getByRole('heading', { name: '分销中心' })).toBeVisible()
  await page.goto('/teams')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
})

test('真实旅行社只能进入团队工作区', async ({ page }) => {
  await tenantLogin(page, 'E2ETRAVEL', 'Travel-E2E-Password-4')
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toHaveCount(0)
  await page.getByRole('menuitem', { name: '旅行社团队' }).click()
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新建团队' })).toBeVisible()
})
