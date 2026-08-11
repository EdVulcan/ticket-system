import { expect, test, type Page } from '@playwright/test'

type Capability = 'supplier' | 'distributor' | 'travel_agency'
type CapabilityExpiry = string | Partial<Record<Capability, string>>
type SupplierBusinessType = string | { business_type: string, status: 'active' | 'suspended' }

async function openAs(page: Page, capability: Capability | Capability[], path: string, expiresAt?: CapabilityExpiry, businessTypes?: SupplierBusinessType[]) {
  const capabilities = Array.isArray(capability) ? capability : [capability]
  const supplierBusinessTypes = businessTypes ?? (capabilities.includes('supplier') && !expiresAt ? ['scenic'] : [])
  const identity = {
    id: 101,
    username: `${capabilities.join('_')}_admin`,
    role: 'admin',
    scope: 'tenant',
    tenant_id: 101,
    tenant_name: '测试商户',
    system_code: 'TEST001',
    capabilities: capabilities.map(capabilityName => ({
      capability: capabilityName,
      status: 'active',
      expires_at: typeof expiresAt === 'string' ? expiresAt : expiresAt?.[capabilityName],
    })),
    supplier_business_types: supplierBusinessTypes.map(item => typeof item === 'string'
      ? { business_type: item, status: 'active' }
      : item),
  }
  await page.addInitScript(({ activeCapabilities, tenantIdentity }) => {
    localStorage.setItem('token', `${activeCapabilities.join('-')}-token`)
    localStorage.setItem('user', JSON.stringify(tenantIdentity))
  }, { activeCapabilities: capabilities, tenantIdentity: identity })
  await page.route('**/api/v1/**', route => {
    const body = route.request().url().endsWith('/api/v1/tenants/me')
      ? { ...identity, id: identity.tenant_id, name: identity.tenant_name }
      : { data: [], total: 0 }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto(path)
}

test('hotel-only supplier cannot enter scenic ticketing workspaces', async ({ page }) => {
  await openAs(page, 'supplier', '/product', undefined, ['hotel'])

  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  for (const label of ['线上门票', '窗口门票', '线下/窗口订单', '旅行社团队', '运营工作台', '政策知识库', '终端设备', '检票点位', '员工管理']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }
  await expect(page.getByRole('menuitem', { name: '系统设置' })).toBeVisible()
})

test('hotel supplier combined with distributor never receives scenic supplier actions', async ({ page }) => {
  await openAs(page, ['supplier', 'distributor'], '/distribution', undefined, ['hotel'])

  await expect(page.getByRole('tab', { name: '我的供应商 (我是分销商)' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '组合产品' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '我的分销商 (我是供应商)' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: '供应履约' })).toHaveCount(0)
})

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

test('expired supplier side is ignored when distributor capability remains active', async ({ page }) => {
  await openAs(page, ['supplier', 'distributor'], '/operations', { supplier: '2020-01-01T00:00:00Z' }, ['scenic'])

  await expect(page.getByRole('tab', { name: '渠道' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '景区' })).toHaveCount(0)
  await page.goto('/report')
  await expect(page.getByRole('tab', { name: '营业汇总' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '核销汇总' })).toHaveCount(0)
})

test('suspended scenic business keeps history workspaces but blocks new sales and onsite fulfillment', async ({ page }) => {
  await openAs(page, 'supplier', '/online-order', undefined, [{ business_type: 'scenic', status: 'suspended' }])

  for (const label of ['线上门票', '窗口门票', '线上订单', '线下/窗口订单', '供销合作', '渠道连接', '旅行社团队', '财务报表', '经营数据', '退款待办', '售后工作台']) {
    await expect(page.getByRole('menuitem', { name: label })).toBeVisible()
  }
  for (const label of ['运营工作台', '政策知识库', '终端设备', '检票点位', '员工管理', '支付参数配置']) {
    await expect(page.getByRole('menuitem', { name: label })).toHaveCount(0)
  }

  let productStatus: Record<string, unknown> | undefined
  await page.route('**/api/v1/products?type=online', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 77, name: '历史线上票', scenic_area_id: 1, price: 80, settlement_price: 60, type: 'online', status: 'online', validity_type: 'relative', validity_days: 1, stock_type: 'unlimited', code_mode: 'order' }] }),
  }))
  await page.route('**/api/v1/products/77/status', async route => {
    productStatus = route.request().postDataJSON()
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
  })

  await page.goto('/product')
  await expect(page.getByRole('heading', { name: '线上门票管理' })).toBeVisible()
  await expect(page.getByRole('button', { name: '发布新门票' })).toHaveCount(0)
  const historicalProduct = page.getByRole('row').filter({ hasText: '历史线上票' })
  await expect(historicalProduct.getByRole('button', { name: '编辑' })).toHaveCount(0)
  await expect(historicalProduct.getByRole('button', { name: '删除' })).toHaveCount(0)
  await historicalProduct.getByRole('button', { name: '下架' }).click()
  await page.getByRole('button', { name: '确定' }).click()
  await expect.poll(() => productStatus).toEqual({ status: 'offline' })

  await page.goto('/product/offline')
  await expect(page.getByRole('heading', { name: '窗口门票管理' })).toBeVisible()
  await expect(page.getByRole('button', { name: '发布窗口票' })).toHaveCount(0)

  await page.goto('/distribution')
  await expect(page.getByRole('tab', { name: '我的分销商 (我是供应商)' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '供应履约' })).toBeVisible()

  await page.goto('/channels')
  await expect(page.getByRole('heading', { name: '渠道连接' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增渠道' })).toHaveCount(0)

  await page.goto('/teams')
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增旅行社合同' })).toHaveCount(0)

  await page.goto('/report')
  await expect(page.getByRole('tab', { name: '核销汇总' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '核销明细' })).toBeVisible()

  for (const path of ['/operations', '/device', '/checkpoint', '/policy', '/staff', '/gate-simulator', '/payment-config']) {
    await page.goto(path)
    await expect(page).toHaveURL('http://127.0.0.1:4173/')
  }
})

test('route guard refreshes tenant capabilities from the backend before authorizing', async ({ page }) => {
  const staleUser = {
    id: 101, username: 'supplier_admin', role: 'admin', scope: 'tenant', tenant_id: 101,
    tenant_name: '测试景区', system_code: 'TEST001',
    capabilities: [{ capability: 'supplier', status: 'active' }],
    supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
  }
  await page.addInitScript(user => {
    localStorage.setItem('token', 'stale-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, staleUser)
  await page.route('**/api/v1/**', route => {
    const body = route.request().url().endsWith('/api/v1/tenants/me')
      ? { id: 101, name: '测试景区', system_code: 'TEST001', capabilities: staleUser.capabilities, supplier_business_types: [{ business_type: 'scenic', status: 'suspended' }] }
      : { data: [], total: 0 }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })

  await page.goto('/operations')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '运营工作台' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '线上订单' })).toBeVisible()
})

test('travel agency distributor bundle creation is online only', async ({ page }) => {
  await openAs(page, ['travel_agency', 'distributor'], '/distribution')

  await expect(page.getByRole('menuitem', { name: '供销合作' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '窗口门票' })).toHaveCount(0)
  await page.getByRole('tab', { name: '组合产品' }).click()
  await expect(page.getByRole('tabpanel', { name: '组合产品' }).getByText('暂无数据')).toBeVisible()
  await page.getByRole('button', { name: '新建组合产品' }).click()

  const dialog = page.getByRole('dialog', { name: '新建组合产品' })
  await expect(dialog.getByRole('radio', { name: '线上' })).toBeChecked()
  await expect(dialog.getByRole('radio', { name: '售票窗口' })).toHaveCount(0)
})
