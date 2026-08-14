import { expect, test, type Page } from '@playwright/test'

type Capability = 'supplier' | 'distributor' | 'travel_agency'
type CapabilityExpiry = string | Partial<Record<Capability, string>>
type SupplierBusinessType = string | { business_type: string, status: 'active' | 'suspended' }

async function openAs(
  page: Page,
  capability: Capability | Capability[],
  path: string,
  expiresAt?: CapabilityExpiry,
  businessTypes?: SupplierBusinessType[],
  access: { role?: string, permissions?: string[], capabilityStatus?: 'active' | 'suspended' } = {},
) {
  const capabilities = Array.isArray(capability) ? capability : [capability]
  const supplierBusinessTypes = businessTypes ?? (capabilities.includes('supplier') && !expiresAt ? ['scenic'] : [])
  const identity = {
    id: 101,
    username: `${capabilities.join('_')}_admin`,
    role: access.role || 'admin',
    scope: 'tenant',
    tenant_id: 101,
    tenant_name: '测试商户',
    system_code: 'TEST001',
    capabilities: capabilities.map(capabilityName => ({
      capability: capabilityName,
      status: access.capabilityStatus || 'active',
      expires_at: typeof expiresAt === 'string' ? expiresAt : expiresAt?.[capabilityName],
    })),
    supplier_business_types: supplierBusinessTypes.map(item => typeof item === 'string'
      ? { business_type: item, status: 'active' }
      : item),
    permissions: access.permissions || [],
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
  await expect(page.getByRole('menuitem', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '系统设置' })).toBeVisible()

  await page.goto('/hotel')
  await expect(page.getByRole('heading', { name: '酒店经营' })).toBeVisible()
})

test('scenic-only supplier cannot see or enter hotel management', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic'])

  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  await expect(page.getByRole('menuitem', { name: '酒店经营' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toBeVisible()
})

test('hotel product operator can read reservations and synchronize fulfillment status', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic', 'hotel'], {
    role: 'product_operator',
    permissions: ['catalog.read', 'catalog.write', 'reports.read', 'hotel_reservations.read', 'hotel_reservations.write', 'hotel_reservations.export'],
  })

  await page.route('**/api/v1/hotels', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 21, code: 'HOTEL01', name: '测试酒店', status: 'active' }] }),
  }))
  await page.route('**/api/v1/scenic-hotel-packages/business-summary**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ net_sales_cents: 99800, gross_sales_cents: 99800, refunded_sales_cents: 0, ticket_component_net_cents: 16000, hotel_component_net_cents: 60000, unallocated_margin_cents: 23800 }),
  }))
  await page.route('**/api/v1/scenic-hotel-packages/reservations**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 31, reservation_no: 'HR202608120001', order_no: 'ORD202608120001', product_name: '双人酒景套餐', guest_name: '测试游客', contact_phone: '13800138000', room_type_name: '山景大床房', rate_plan_name: '含双早', check_in_date: '2026-08-20', check_out_date: '2026-08-21', rooms: 1, status: 'confirmed' }], total: 1 }),
  }))
  let checkedIn = false
  await page.route('**/api/v1/scenic-hotel-packages/reservations/31/status', async route => {
    checkedIn = (await route.request().postDataJSON()).status === 'checked_in'
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'ok' }) })
  })
  await page.reload()

  await expect(page.getByRole('menuitem', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '线上门票' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '酒景套餐' })).toBeVisible()
  await page.getByRole('tab', { name: '酒景套餐' }).click()
  await expect(page.getByText('套餐配置')).toBeVisible()
  await expect(page.getByText('住宿预订')).toBeVisible()
  await page.getByText('住宿预订', { exact: true }).click()
  await expect(page.getByText('净销售额', { exact: true })).toBeVisible()
  await expect(page.getByText('双人酒景套餐')).toBeVisible()
  await expect(page.getByText('销售按付款期归属，后续退款会回写原付款期的最终净额')).toBeVisible()
  await page.getByRole('button', { name: '登记已入住' }).click()
  await expect.poll(() => checkedIn).toBe(true)
})

test('viewer can inspect hotel catalog without receiving guest reservation data', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic', 'hotel'], {
    role: 'viewer', permissions: ['catalog.read', 'reports.read'],
  })
  let reservationRequested = false
  let syncFailureRequested = false
  await page.route('**/api/v1/hotels', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 21, code: 'HOTEL01', name: '测试酒店', status: 'active' }] }),
  }))
  await page.route('**/api/v1/scenic-hotel-packages/reservations**', route => {
    reservationRequested = true
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [] }) })
  })
  await page.route('**/api/v1/scenic-hotel-packages/booking-sync-operations/failed**', route => {
    syncFailureRequested = true
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [], total: 0, page: 1, page_size: 20 }) })
  })
  await page.reload()

  await expect(page.getByRole('heading', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增酒店' })).toHaveCount(0)
  await page.getByRole('tab', { name: '酒景套餐' }).click()
  await expect(page.getByText('套餐配置')).toBeVisible()
  await expect(page.getByText('住宿预订', { exact: true })).toHaveCount(0)
  await expect(page.getByText('预约同步异常', { exact: true })).toHaveCount(0)
  await expect.poll(() => reservationRequested).toBe(false)
  await expect.poll(() => syncFailureRequested).toBe(false)
})

test('suspended supplier can still fulfill confirmed hotel reservations', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic', 'hotel'], {
    role: 'product_operator',
    permissions: ['catalog.read', 'catalog.write', 'reports.read', 'hotel_reservations.read', 'hotel_reservations.write', 'hotel_reservations.export'],
    capabilityStatus: 'suspended',
  })
  await page.route('**/api/v1/hotels', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [{ id: 21, code: 'HOTEL01', name: '测试酒店', status: 'active' }] }) }))
  await page.route('**/api/v1/scenic-hotel-packages/reservations**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 31, reservation_no: 'HR1', order_no: 'ORD1', product_name: '历史套餐', guest_name: '测试游客', contact_phone: '13800138000', check_in_date: '2026-08-20', check_out_date: '2026-08-21', rooms: 1, status: 'confirmed' }], total: 1 }),
  }))
  let checkedIn = false
  await page.route('**/api/v1/scenic-hotel-packages/reservations/31/status', async route => {
    checkedIn = (await route.request().postDataJSON()).status === 'checked_in'
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'ok' }) })
  })
  await page.reload()

  await expect(page).toHaveURL('http://127.0.0.1:4173/hotel')
  await page.getByRole('tab', { name: '酒景套餐' }).click()
  await page.getByText('住宿预订', { exact: true }).click()
  await expect(page.getByText('历史套餐')).toBeVisible()
  await expect(page.getByRole('button', { name: '登记已入住' })).toBeVisible()
  await page.getByRole('button', { name: '登记已入住' }).click()
  await expect.poll(() => checkedIn).toBe(true)
})

test('hotel reservation operator can inspect and retry failed Xiaohongshu booking synchronization', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic', 'hotel'], {
    role: 'hotel_operator',
    permissions: ['catalog.read', 'hotel_reservations.read', 'hotel_reservations.write'],
  })

  await page.route('**/api/v1/hotels', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 21, code: 'HOTEL01', name: '测试酒店', status: 'active' }] }),
  }))
  let listRequests = 0
  await page.route('**/api/v1/scenic-hotel-packages/booking-sync-operations/failed**', route => {
    listRequests += 1
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        data: [{
          id: 81,
          type: 'book',
          status: 'failed',
          failed_from_stage: 'confirm_pending',
          external_book_order_id: 'SECRET-EXTERNAL-ID',
          platform_book_id: 'SECRET-PLATFORM-ID',
          attempts: 3,
          max_attempts: 20,
          last_error: '小红书预约确认超时，请检查渠道连接',
          updated_at: '2026-08-13T09:30:00+08:00',
          completed_at: '2026-08-13T09:30:00+08:00',
          entitlement_no: 'ENT202608130001',
          order_no: 'ORD202608130001',
        }],
        total: 1,
        page: 1,
        page_size: 20,
      }),
    })
  })
  let retryReason = ''
  await page.route('**/api/v1/scenic-hotel-packages/booking-sync-operations/81/retry', async route => {
    retryReason = String((await route.request().postDataJSON()).reason || '')
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'booking synchronization retry queued' }) })
  })
  await page.reload()

  await page.getByRole('tab', { name: '酒景套餐' }).click()
  await page.getByText('预约同步异常', { exact: true }).click()
  await expect(page.getByText('ORD202608130001')).toBeVisible()
  await expect(page.getByText('ENT202608130001')).toBeVisible()
  await expect(page.getByText('小红书预约确认超时，请检查渠道连接')).toBeVisible()
  await expect(page.getByText('SECRET-EXTERNAL-ID')).toHaveCount(0)
  await expect(page.getByText('SECRET-PLATFORM-ID')).toHaveCount(0)

  await page.getByRole('button', { name: '继续重试' }).click()
  const retryDialog = page.getByRole('dialog', { name: '继续重试小红书同步' })
  await expect(retryDialog.getByText('预约确认', { exact: true })).toBeVisible()
  await expect(retryDialog.getByText('平台确认后本地落地', { exact: true })).toBeVisible()
  await expect(retryDialog.getByText('小红书预约确认超时，请检查渠道连接')).toBeVisible()
  await expect(retryDialog.getByRole('button', { name: '继续重试' })).toBeDisabled()
  await retryDialog.getByRole('textbox').fill('渠道连接已恢复，继续处理该预约')
  await retryDialog.getByRole('button', { name: '继续重试' }).click()

  await expect.poll(() => retryReason).toBe('渠道连接已恢复，继续处理该预约')
  await expect.poll(() => listRequests).toBeGreaterThan(1)
})

test('hotel reservation reader sees synchronization failures without retry action', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, ['scenic', 'hotel'], {
    role: 'hotel_viewer',
    permissions: ['catalog.read', 'hotel_reservations.read'],
  })
  await page.route('**/api/v1/hotels', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 21, code: 'HOTEL01', name: '测试酒店', status: 'active' }] }),
  }))
  await page.route('**/api/v1/scenic-hotel-packages/booking-sync-operations/failed**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 82, type: 'refund', status: 'failed', failed_from_stage: 'pending', attempts: 20, max_attempts: 20, last_error: '退款通知失败', updated_at: '2026-08-13T10:00:00+08:00', entitlement_no: 'ENT82', order_no: 'ORD82' }], total: 1, page: 1, page_size: 20 }),
  }))
  await page.reload()

  await page.getByRole('tab', { name: '酒景套餐' }).click()
  await page.getByText('预约同步异常', { exact: true }).click()
  await expect(page.locator('.el-table__body-wrapper').getByText('退款同步', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '继续重试' })).toHaveCount(0)
})

test('suspended hotel business keeps catalog read access but hides write actions', async ({ page }) => {
  await openAs(page, 'supplier', '/hotel', undefined, [{ business_type: 'hotel', status: 'suspended' }])

  await expect(page.getByRole('menuitem', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '酒店经营' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增酒店' })).toHaveCount(0)
  await expect(page.getByText('供应商身份或酒店住宿业态已暂停，当前仅可查看历史配置与已有预订。')).toBeVisible()
})

test('hotel supplier combined with distributor never receives scenic supplier actions', async ({ page }) => {
  await openAs(page, ['supplier', 'distributor'], '/distribution', undefined, ['hotel'])

  await expect(page.getByRole('tab', { name: '我的供应商 (我是分销商)' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '组合产品' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '我的分销商 (我是供应商)' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: '供应履约' })).toHaveCount(0)
})

test('distributor cannot see or enter supplier onsite business', async ({ page }) => {
  test.slow()
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
    await expect(page).toHaveURL('http://127.0.0.1:4173/', { timeout: 15_000 })
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
  test.slow()
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
    await expect(page).toHaveURL('http://127.0.0.1:4173/', { timeout: 15_000 })
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
