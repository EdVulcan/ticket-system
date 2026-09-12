import { expect, test, type Page, type Route } from '@playwright/test'

const user = { id: 8, role: 'super_admin', scope: 'tenant', tenant_id: 1, tenant_name: '测试景区',
  capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
const account = { id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production' }
const order = { order_no: 'XHS-REFUND-TEST', external_no: 'EXTERNAL-TEST', channel: 'xiaohongshu', environment: 'production', status: 'paid', total_amount: 80,
  items: [{ product_name: '测试门票', quantity: 1, tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'unused', check_in_count: 0, pending_refund_id: 0 }] }] }
const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
const detailPath = '**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST'

const refundDetail = (refunds: unknown[]) => ({
  order, payments: [{ method: 'xiaohongshu', status: 'paid', amount_cents: 8000 }], refunds, after_sales: [], check_ins: [],
})

async function prepare(page: Page, role = 'super_admin', identity: Record<string, unknown> = {}) {
  await page.addInitScript(value => {
    localStorage.setItem('token', 'test-token')
    localStorage.setItem('user', JSON.stringify(value))
  }, { ...user, role, permissions: role === 'viewer' ? ['channels.read', 'orders.read'] : [], ...identity })
  await page.route('**/api/v1/tenants/me', route => json(route, { ...user, id: 1, status: 'active' }))
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [account] }))
  await page.route('**/api/v1/channel-accounts/7/orders?*', route => json(route, { data: [{ ...order, ticket_count: 1, paid_cents: 8000 }], total: 1 }))
  await page.route(detailPath, route => json(route, refundDetail([])))
  await page.goto('/channels')
  await page.getByRole('button', { name: '渠道订单', exact: true }).click()
}

test('渠道订单提供退款入口，pending 不误报到账，网络重试复用申请', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await prepare(page)
  let calls = 0
  let original: any
  await page.route('**/api/v1/payments/refunds/mixed', async route => {
    const body = route.request().postDataJSON()
    expect(body.order_no).toBe(order.order_no)
    expect(body.ticket_codes).toEqual(['DEMO-NOT-VALID'])
    expect(body.amount).toBe(80)
    expect(body.reason).toBe('游客行程改变')
    expect(body).not.toHaveProperty('override_refund_policy')
    calls++
    if (calls === 1) { original = body; await route.abort('failed'); return }
    expect(body).toEqual(original)
    await json(route, { id: 9, status: 'pending', method: 'xiaohongshu' }, 201)
  })
  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await expect(dialog.getByText('¥80.00', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认申请退款' })).toBeDisabled()
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await dialog.getByRole('button', { name: '重试同一申请', exact: true }).click()
  await expect(page.getByText('退款申请已提交，等待原支付渠道确认，请刷新查看进度', { exact: true })).toBeVisible()
  await expect(page.getByText('退款已完成', { exact: true })).toHaveCount(0)
  expect(calls).toBe(2)
  expect(errors).toEqual([])
})

for (const entry of ['channel', 'online']) {
  test(`${entry} 不可退旧单由初始管理员明确确认，取消不提交，重试保留例外申请`, async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await prepare(page, 'super_admin', { is_initial_admin: true })
    const oldOrder = { ...order, tenant_id: 1, items: order.items.map(item => ({ ...item, refund_type: 'no_refund' })) }
    await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, { order: oldOrder, refunds: [] }))
    if (entry === 'online') {
      await page.route('**/api/v1/orders?*', route => json(route, { data: [oldOrder], total: 1, channel_options: [] }))
      await page.route('**/api/v1/orders/XHS-REFUND-TEST', route => json(route, { order: oldOrder, refunds: [] }))
      await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [] }))
      await page.route('**/api/v1/devices?*', route => json(route, { data: [] }))
      await page.goto('/online-order')
    }
    let calls = 0
    let original: any
    await page.route('**/api/v1/payments/refunds/mixed', async route => {
      const body = route.request().postDataJSON()
      expect(body).toMatchObject({ order_no: order.order_no, amount: 80, reason: '售出时退票政策设置错误', override_refund_policy: true, ticket_codes: ['DEMO-NOT-VALID'] })
      calls++
      if (calls === 1) { original = body; await route.abort('failed'); return }
      expect(body).toEqual(original)
      await json(route, { id: 9, status: 'pending' }, 201)
    })
    await page.getByRole('button', { name: entry === 'online' ? '退款' : '申请退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    const checkbox = dialog.getByRole('checkbox', { name: '以初始管理员身份申请例外退款' })
    await expect(checkbox).not.toBeChecked()
    await dialog.getByPlaceholder('请填写本次例外退款的具体原因').fill('售出时退票政策设置错误')
    await expect(dialog.getByRole('button', { name: '确认例外退款', exact: true })).toBeDisabled()
    await dialog.locator('label.el-checkbox').click()
    await expect(checkbox).toBeChecked()
    await page.screenshot({ path: `${process.env.TEMP || '/tmp'}/refund-policy-${entry}.png` })
    await dialog.getByRole('button', { name: '确认例外退款', exact: true }).click()
    const confirmation = page.getByRole('dialog', { name: '确认管理员例外退款' })
    await expect(confirmation).toContainText('¥80.00')
    await expect(confirmation).toContainText('售出时退票政策设置错误')
    await confirmation.getByRole('button', { name: '返回检查' }).click()
    expect(calls).toBe(0)
    await dialog.getByRole('button', { name: '确认例外退款', exact: true }).click()
    await confirmation.getByRole('button', { name: '确认提交例外退款' }).click()
    await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
    await expect(checkbox).toBeDisabled()
    await dialog.getByRole('button', { name: '重试同一申请', exact: true }).click()
    await expect(page.getByText('退款申请已提交，等待原支付渠道确认，请刷新查看进度', { exact: true })).toBeVisible()
    expect(calls).toBe(2)
    expect(errors).toEqual([])
  })
}

for (const scenario of [
  { entry: 'channel', failure: 'abort', recoveredStatus: 'succeeded', expectedMessage: '退款已完成' },
  { entry: 'online', failure: '5xx', recoveredStatus: 'pending', expectedMessage: '退款申请已提交，等待原支付渠道确认，请刷新查看进度' },
  { entry: 'channel', failure: 'generic400', recoveredStatus: 'succeeded', expectedMessage: '退款已完成' },
] as const) {
  test(`${scenario.entry}订单在不确定退款提交后自动查询原详情且不自动重放（${scenario.failure}）`, async ({ page }) => {
    await prepare(page)
    let detailReads = 0
    let postCalls = 0
    let original: any
    const detailURL = scenario.entry === 'channel'
      ? detailPath
      : '**/api/v1/orders/XHS-REFUND-TEST'
    await page.route(detailURL, route => {
      detailReads++
      const refunds = detailReads === 1 ? [] : [{
        order_no: order.order_no,
        idempotency_key: original?.idempotency_key,
        status: scenario.recoveredStatus,
      }]
      return json(route, refundDetail(refunds))
    })
    if (scenario.entry === 'online') {
      await page.route('**/api/v1/orders?*', route => json(route, { data: [order], total: 1, channel_options: [] }))
      await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [] }))
      await page.route('**/api/v1/devices?*', route => json(route, { data: [] }))
      await page.goto('/online-order')
    }
    await page.route('**/api/v1/payments/refunds/mixed', async route => {
      postCalls++
      original = route.request().postDataJSON()
      if (scenario.failure === 'abort') await route.abort('failed')
      else if (scenario.failure === 'generic400') await json(route, { error: 'unexpected EOF' }, 400)
      else await json(route, { error: '上游暂不可用' }, 503)
    })

    await page.getByRole('button', { name: scenario.entry === 'channel' ? '申请退款' : '退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
    await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()

    await expect(page.getByText(scenario.expectedMessage, { exact: true })).toBeVisible()
    await expect(page.getByText('请求失败，请稍后重试', { exact: true })).toHaveCount(0)
    await expect(page.getByText('网络连接异常', { exact: true })).toHaveCount(0)
    await expect(page.locator('.el-message--error')).toHaveCount(0)
    expect(postCalls).toBe(1)
    expect(detailReads).toBe(2)
  })
}

test('渠道订单只按订单号和幂等键查询不确定退款，重复查询不会自动重放', async ({ page }) => {
  await prepare(page)
  let detailReads = 0
  let postCalls = 0
  let original: any
  await page.route(detailPath, route => {
    detailReads++
    const refunds = detailReads === 1 ? [] : detailReads === 2
      ? [{ order_no: order.order_no, idempotency_key: 'another-refund', status: 'succeeded' }]
      : detailReads === 3
        ? [{ order_no: 'OTHER-ORDER', idempotency_key: original?.idempotency_key, status: 'succeeded' }]
        : []
    return json(route, refundDetail(refunds))
  })
  await page.route('**/api/v1/payments/refunds/mixed', async route => {
    postCalls++
    original = route.request().postDataJSON()
    await route.abort('failed')
  })

  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await expect(page.getByText('退款已完成', { exact: true })).toHaveCount(0)
  expect(detailReads).toBe(2)

  await dialog.getByRole('button', { name: '查询退款结果', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await dialog.getByRole('button', { name: '查询退款结果', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await expect(page.getByText('退款已完成', { exact: true })).toHaveCount(0)
  expect(postCalls).toBe(1)
  expect(detailReads).toBe(4)
})

test('明确的退款校验拒绝保持可见且不进入不确定查询', async ({ page }) => {
  await prepare(page)
  let detailReads = 0
  await page.route(detailPath, route => {
    detailReads++
    return json(route, refundDetail([]))
  })
  await page.route('**/api/v1/payments/refunds/mixed', route => json(route, { error: '退款金额必须与票券金额一致' }, 400))

  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()

  await expect(dialog.getByText('退款金额必须与票券金额一致', { exact: true })).toBeVisible()
  await expect(dialog.getByText(/退款结果暂未确认/)).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: '查询退款结果', exact: true })).toHaveCount(0)
  expect(detailReads).toBe(1)
})

test('退款处理中提示使用业务 warning 样式而不是 error', async ({ page }) => {
  await prepare(page)
  await page.route('**/api/v1/payments/refunds/mixed', route => json(route, { error: '订单正在退款或核销处理中，请勿重复申请' }, 409))

  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()

  await expect(dialog.locator('.el-alert--warning')).toContainText('订单正在退款或核销处理中，请勿重复申请')
  await expect(dialog.locator('.el-alert--error')).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: '查询退款结果', exact: true })).toHaveCount(0)
})

test('渠道订单的不确定退款查询失败仍保持未确认且不重放', async ({ page }) => {
  await prepare(page)
  let detailReads = 0
  let postCalls = 0
  await page.route(detailPath, route => {
    detailReads++
    if (detailReads === 1) return json(route, refundDetail([]))
    return json(route, { error: '订单查询暂不可用' }, 503)
  })
  await page.route('**/api/v1/payments/refunds/mixed', async route => {
    postCalls++
    await route.abort('failed')
  })

  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
  await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await dialog.getByRole('button', { name: '查询退款结果', exact: true }).click()
  await expect(dialog.getByText(/退款结果暂未确认/)).toBeVisible()
  await expect(page.getByText('请求失败，请稍后重试', { exact: true })).toHaveCount(0)
  expect(postCalls).toBe(1)
  expect(detailReads).toBe(3)
})

for (const entry of ['channel', 'online'] as const) {
  test(`${entry}订单已接受退款后列表刷新 503 不改写已提交结果`, async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await prepare(page)
    let refreshCalls = 0
    if (entry === 'channel') {
      await page.route('**/api/v1/channel-accounts/7/orders?*', route => {
        refreshCalls++
        return json(route, { error: '订单列表刷新失败' }, 503)
      })
    } else {
      await page.route('**/api/v1/orders?*', route => {
        refreshCalls++
        return refreshCalls === 1
          ? json(route, { data: [order], total: 1, channel_options: [] })
          : json(route, { error: '订单列表刷新失败' }, 503)
      })
      await page.route('**/api/v1/orders/XHS-REFUND-TEST', route => json(route, refundDetail([])))
      await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [] }))
      await page.route('**/api/v1/devices?*', route => json(route, { data: [] }))
      await page.goto('/online-order')
    }
    let postCalls = 0
    await page.route('**/api/v1/payments/refunds/mixed', route => {
      postCalls++
      return json(route, { id: 9, status: 'pending', method: 'xiaohongshu' }, 201)
    })

    await page.getByRole('button', { name: entry === 'channel' ? '申请退款' : '退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程改变')
    await dialog.getByRole('button', { name: '确认申请退款', exact: true }).click()
    await expect(page.getByText('退款申请已提交，等待原支付渠道确认，请刷新查看进度', { exact: true })).toBeVisible()
    await expect(page.getByText(entry === 'channel'
      ? '退款结果已返回，但订单信息刷新失败，请手动刷新查看'
      : '退款结果已返回，但订单列表刷新失败，请手动刷新查看', { exact: true })).toBeVisible()
    await expect(page.locator('.el-message--error')).toHaveCount(0)
    expect(refreshCalls).toBe(entry === 'channel' ? 1 : 2)
    expect(errors).toEqual([])
    expect(postCalls).toBe(1)
  })
}

for (const scenario of [
  { name: '普通管理员', identity: { is_initial_admin: false }, tenantId: 1 },
  { name: '其他销售租户', identity: { is_initial_admin: true }, tenantId: 2 },
  { name: '纯分销商', identity: { is_initial_admin: true, capabilities: [{ capability: 'distributor', status: 'active' }], supplier_business_types: [] }, tenantId: 1 },
]) {
  test(`${scenario.name}不可通过共享退款弹窗申请政策例外`, async ({ page }) => {
    await prepare(page, 'super_admin', scenario.identity)
    // Preserve this identity through the router's tenant refresh.
    await page.route('**/api/v1/tenants/me', route => json(route, { ...user, ...scenario.identity, id: 1, status: 'active' }))
    await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, {
      order: { ...order, tenant_id: scenario.tenantId, items: order.items.map(item => ({ ...item, refund_type: 'no_refund' })) }, refunds: [],
    }))
    // Identity is read afresh when the refund dialog opens.
    await page.evaluate(identity => localStorage.setItem('user', JSON.stringify({ ...JSON.parse(localStorage.getItem('user') || '{}'), ...identity })), scenario.identity)
    let calls = 0
    await page.route('**/api/v1/payments/refunds/mixed', route => { calls++; return json(route, {}) })
    await page.getByRole('button', { name: '申请退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    await expect(dialog.getByText('该订单购买时不可退，仅本商户景区初始管理员可申请例外退款')).toBeVisible()
    await expect(dialog.getByRole('checkbox')).toHaveCount(0)
    await expect(dialog.getByRole('button', { name: '确认例外退款', exact: true })).toBeDisabled()
    expect(calls).toBe(0)
  })
}

test('初始管理员的政策例外不放开未知或处理中票券', async ({ page }) => {
  await prepare(page, 'super_admin', { is_initial_admin: true })
  let calls = 0
  await page.route('**/api/v1/payments/refunds/mixed', route => { calls++; return json(route, {}) })
  for (const ticket of [
    { ticket_code: 'DEMO-NOT-VALID', status: 'unused' },
    { ticket_code: '', status: 'unused', check_in_count: 0 },
    { ticket_code: 'DEMO-NOT-VALID', status: 'unused', check_in_count: 0, pending_refund_id: 9 },
    { ticket_code: 'DEMO-NOT-VALID', status: 'unused', check_in_count: 0, pending_xiaohongshu_verification_id: 9 },
  ]) {
    await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, {
      order: { ...order, tenant_id: 1, items: [{ refund_type: 'no_refund', tickets: [ticket] }] }, refunds: [],
    }))
    await page.getByRole('button', { name: '申请退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    await expect(dialog.getByRole('alert')).toBeVisible()
    await expect(dialog.getByRole('checkbox')).toHaveCount(0)
    await expect(dialog.getByRole('button', { name: '确认例外退款', exact: true })).toBeDisabled()
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
  }
  expect(calls).toBe(0)
})

for (const entry of ['channel', 'online'] as const) {
  test(`${entry} 初始供应商管理员可为已完成的小红书误核销订单提交整单退款`, async ({ page }) => {
    await prepare(page, 'super_admin', { is_initial_admin: true })
    const usedOrder = {
      ...order,
      tenant_id: 1,
      status: 'completed',
      items: [{ product_name: '测试门票', quantity: 1, tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'used', check_in_count: 1, pending_refund_id: 0 }] }],
    }
    const usedDetail = { order: usedOrder, payments: [{ method: 'xiaohongshu', status: 'paid', amount_cents: 8000 }], refunds: [], after_sales: [], check_ins: [] }
    await page.route(detailPath, route => json(route, usedDetail))
    await page.route('**/api/v1/channel-accounts/7/orders?*', route => json(route, { data: [{ ...usedOrder, ticket_count: 1, paid_cents: 8000, used_ticket_count: 1, refunded_ticket_count: 0 }], total: 1 }))
    if (entry === 'online') {
      await page.route('**/api/v1/orders?*', route => json(route, { data: [usedOrder], total: 1, channel_options: [] }))
      await page.route('**/api/v1/orders/XHS-REFUND-TEST', route => json(route, usedDetail))
      await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [] }))
      await page.route('**/api/v1/devices?*', route => json(route, { data: [] }))
      await page.goto('/online-order')
    } else {
      await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '刷新', exact: true }).click()
    }

    let body: any
    await page.route('**/api/v1/payments/refunds/mixed', async route => {
      body = route.request().postDataJSON()
      await json(route, { id: 10, status: 'pending', method: 'xiaohongshu' }, 201)
    })
    await page.getByRole('button', { name: entry === 'channel' ? '申请退款' : '退款', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '申请原路退款' })
    await expect(dialog.getByText('订单包含已核销票券。本次仅作为误核销纠错')).toBeVisible()
    const acknowledgement = dialog.getByRole('checkbox', { name: '已确认误核销，退款成功后票券失效，并保留原核销记录' })
    await expect(acknowledgement).not.toBeChecked()
    await dialog.getByPlaceholder('请填写本次例外退款的具体原因').fill('工作人员误核销，确认退票')
    await expect(dialog.getByRole('button', { name: '确认例外退款', exact: true })).toBeDisabled()
    await dialog.locator('label.el-checkbox').click()
    await page.screenshot({ path: `${process.env.TEMP || '/tmp'}/xiaohongshu-used-refund-${entry}.png` })
    await dialog.getByRole('button', { name: '确认例外退款', exact: true }).click()
    const confirmation = page.getByRole('dialog', { name: '确认管理员例外退款' })
    await expect(confirmation).toContainText('原核销记录会保留，退款成功后票券失效')
    await confirmation.getByRole('button', { name: '确认提交例外退款' }).click()
    await expect(page.getByText('退款申请已提交，等待原支付渠道确认，请刷新查看进度', { exact: true })).toBeVisible()
    expect(body).toMatchObject({
      order_no: order.order_no,
      ticket_codes: ['DEMO-NOT-VALID'],
      amount: 80,
      reason: '工作人员误核销，确认退票',
    })
    expect(body).not.toHaveProperty('override_refund_policy')
  })
}

test('非初始管理员不显示已完成小红书订单的退款入口', async ({ page }) => {
  await prepare(page)
  const usedOrder = { ...order, tenant_id: 1, status: 'completed' }
  await page.route('**/api/v1/channel-accounts/7/orders?*', route => json(route, { data: [{ ...usedOrder, ticket_count: 1, paid_cents: 8000 }], total: 1 }))
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByRole('button', { name: '申请退款', exact: true })).toHaveCount(0)
})

test('已核销退款在票券或退款处理中保持禁止提交', async ({ page }) => {
  await prepare(page, 'super_admin', { is_initial_admin: true })
  const usedOrder = {
    ...order,
    tenant_id: 1,
    status: 'completed',
    items: [{ product_name: '测试门票', quantity: 1, tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'used', check_in_count: 1, pending_xiaohongshu_verification_id: 9 }] }],
  }
  await page.route(detailPath, route => json(route, { order: usedOrder, refunds: [] }))
  await page.route('**/api/v1/channel-accounts/7/orders?*', route => json(route, { data: [{ ...usedOrder, ticket_count: 1, paid_cents: 8000 }], total: 1 }))
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '刷新', exact: true }).click()
  await page.getByRole('button', { name: '申请退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await expect(dialog.getByText('订单正在退款或核销处理中，请勿重复申请')).toBeVisible()
  await expect(dialog.getByRole('checkbox')).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: '确认例外退款', exact: true })).toBeDisabled()
})

test('渠道订单详情对非初始管理员拒绝已用票退款', async ({ page }) => {
  await prepare(page)
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '详情', exact: true }).click()
  const detail = page.getByRole('dialog', { name: '渠道订单详情', exact: true })
  await expect(detail.getByRole('button', { name: '申请退款' })).toBeVisible()
  await page.route('**/api/v1/channel-accounts/7/orders/XHS-REFUND-TEST', route => json(route, {
    order: { ...order, items: [{ tickets: [{ ticket_code: 'DEMO-NOT-VALID', status: 'active', check_in_count: 1 }] }] }, refunds: [],
  }))
  await detail.getByRole('button', { name: '申请退款' }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await expect(dialog.getByText('订单包含已核销票券，仅本商户景区初始管理员可按误核销例外申请退款')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认例外退款' })).toBeDisabled()
})

test('只读岗位在渠道列表及详情没有退款写入口', async ({ page }) => {
  await prepare(page, 'viewer')
  await expect(page.getByRole('button', { name: '申请退款', exact: true })).toHaveCount(0)
  await page.getByRole('dialog', { name: '渠道订单：xhs-qa' }).getByRole('button', { name: '详情', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '渠道订单详情', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '申请退款', exact: true })).toHaveCount(0)
})
