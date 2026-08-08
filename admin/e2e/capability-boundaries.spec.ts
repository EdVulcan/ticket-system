import { expect, test, type Page } from '@playwright/test'

type Capability = 'supplier' | 'distributor' | 'travel_agency'

async function openAs(page: Page, capability: Capability, path: string, expiresAt?: string) {
  await page.addInitScript(({ activeCapability, capabilityExpiresAt }) => {
    localStorage.setItem('token', `${activeCapability}-token`)
    localStorage.setItem('user', JSON.stringify({
      id: 101,
      username: `${activeCapability}_admin`,
      role: 'admin',
      scope: 'tenant',
      tenant_id: 101,
      tenant_name: '测试商户',
      system_code: 'TEST001',
      capabilities: [{ capability: activeCapability, status: 'active', expires_at: capabilityExpiresAt }],
    }))
  }, { activeCapability: capability, capabilityExpiresAt: expiresAt })
  await page.route('**/api/v1/**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [], total: 0 }),
  }))
  await page.goto(path)
}

test('distributor cannot see or enter supplier onsite business', async ({ page }) => {
  await openAs(page, 'distributor', '/operations')

  for (const label of ['线上订单', '供销合作', '渠道连接', '运营工作台', '财务报表', '经营数据', '退款待办', '售后工作台', '管理账号', '支付参数配置']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['线上门票', '窗口门票', '线下/窗口订单', '旅行社团队', '政策知识库', '终端设备', '检票点位', '员工管理']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }

  for (const label of ['渠道', '结算', '总账']) {
    await expect(page.getByRole('tab', { name: label })).toBeVisible()
  }
  for (const label of ['景区', '团队', '班次', '打印', '告警']) {
    await expect(page.getByRole('tab', { name: label })).toHaveCount(0)
  }

  for (const path of ['/product', '/offline-order', '/device', '/checkpoint', '/policy', '/staff', '/gate-simulator']) {
    await page.goto(path)
    await expect(page).toHaveURL('http://127.0.0.1:4173/')
  }
})

test('travel agency only receives team, order and report surfaces', async ({ page }) => {
  await openAs(page, 'travel_agency', '/teams')

  for (const label of ['线上订单', '旅行社团队', '经营数据', '售后工作台', '管理账号']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['线上门票', '窗口门票', '线下/窗口订单', '供销合作', '渠道连接', '运营工作台', '财务报表', '退款待办', '政策知识库', '终端设备', '检票点位', '员工管理', '支付参数配置']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }

  await expect(page.getByRole('tab', { name: '团队计划' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '旅行社合同' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '双方结算' })).toBeVisible()

  for (const path of ['/product', '/offline-order', '/distribution', '/channels', '/operations', '/finance', '/device', '/payment-config', '/staff']) {
    await page.goto(path)
    await expect(page).toHaveURL('http://127.0.0.1:4173/')
  }
})

test('non-supplier report hides verification income tabs', async ({ page }) => {
  await openAs(page, 'distributor', '/report')

  await expect(page.getByRole('tab', { name: '营业汇总' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '营业明细' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '核销汇总' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: '核销明细' })).toHaveCount(0)
})

test('expired capability is hidden and cannot pass the route guard', async ({ page }) => {
  await openAs(page, 'supplier', '/device', '2020-01-01T00:00:00Z')

  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  await expect(page.getByRole('menuitem', { name: '终端设备' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '线下/窗口订单' })).toHaveCount(0)
})
