import { expect, test, type Page } from '@playwright/test'

const permissions: Record<string, string[]> = {
  product_operator: ['catalog.read', 'catalog.write', 'orders.read', 'after_sales.read', 'after_sales.write', 'distribution.read', 'distribution.write', 'channels.read', 'channels.write', 'reports.read', 'operations.read', 'onsite.read'],
  team_operator: ['orders.read', 'after_sales.read', 'after_sales.write', 'teams.read', 'teams.write', 'reports.read', 'operations.read', 'onsite.read', 'finance.read', 'settlements.read'],
  settlement_operator: ['orders.read', 'after_sales.read', 'finance.read', 'finance.write', 'settlements.read', 'settlements.write', 'refunds.read', 'refunds.write', 'reports.read', 'operations.read', 'payments.read', 'teams.read'],
  viewer: ['catalog.read', 'orders.read', 'after_sales.read', 'distribution.read', 'channels.read', 'teams.read', 'finance.read', 'settlements.read', 'refunds.read', 'reports.read', 'operations.read', 'onsite.read', 'payments.read'],
}

async function openAs(page: Page, role: string, capabilities: string[], path = '/') {
  await page.addInitScript(({ activeRole, activePermissions, activeCapabilities }) => {
    localStorage.setItem('token', `${activeRole}-token`)
    localStorage.setItem('user', JSON.stringify({
      id: 8, username: activeRole, role: activeRole, scope: 'tenant', tenant_id: 8,
      tenant_name: '岗位测试商户', system_code: 'ROLE008', permissions: activePermissions,
      capabilities: activeCapabilities.map(capability => ({ capability, status: 'active' })),
    }))
  }, { activeRole: role, activePermissions: permissions[role], activeCapabilities: capabilities })
  await page.route('**/api/v1/**', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [], total: 0 }) }))
  await page.goto(path)
}

test('产品运营只进入产品、订单、分销和渠道工作区', async ({ page }) => {
  await openAs(page, 'product_operator', ['supplier', 'distributor'])
  for (const label of ['线上门票', '窗口门票', '线上订单', '分销商管理', '渠道连接', '运营工作台', '经营数据', '售后工作台']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['旅行社团队', '财务报表', '退款待办', '员工管理', '管理账号', '支付参数配置']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }
  await page.goto('/finance')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
})

test('团队业务员不获得分销、渠道和账号管理权限', async ({ page }) => {
  await openAs(page, 'team_operator', ['travel_agency'])
  for (const label of ['线上订单', '旅行社团队', '经营数据', '售后工作台']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['线上门票', '分销商管理', '渠道连接', '运营工作台', '财务报表', '退款待办', '管理账号']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }
  await page.goto('/distribution')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
})

test('结算对账员能处理对账但不能维护产品和账号', async ({ page }) => {
  await openAs(page, 'settlement_operator', ['supplier', 'travel_agency'])
  for (const label of ['线上订单', '旅行社团队', '财务报表', '经营数据', '退款待办', '售后工作台']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['线上门票', '窗口门票', '分销商管理', '渠道连接', '员工管理', '管理账号', '支付参数配置']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }
  await page.goto('/system-user')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
})
