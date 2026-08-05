import { expect, test, type Page } from '@playwright/test'

const staff = { id: 7, name: '测试售票员', job_number: 'SELLER001', roles: 'seller', tenant_id: 1 }
const checkerStaff = { id: 8, name: '测试验票员', job_number: 'CHECKER001', roles: 'checker', tenant_id: 1 }
const product = { id: 11, name: '标准成人票', price: 80, stock_type: 'unlimited', daily_stock: 0, tags: '["当日"]' }

const bundle = { id: 21, name: '双景区联票', retail_price_cents: 15000, type: 'offline', status: 'online', is_bundle: true }

async function json(route: import('@playwright/test').Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockPOSBoot(page: Page, openShift: boolean) {
  await page.route('**/api/v1/products?*', route => json(route, { data: [product], total: 1 }))
  await page.route('**/api/v1/bundle-catalog?*', route => json(route, { data: [bundle] }))
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [{ id: 31, name: '东门检票点' }], total: 1 }))
  await page.route('**/api/v1/operations/shifts/open?*', route => openShift
    ? json(route, { id: 41, opened_at: '2026-08-01T08:00:00Z', opening_cents: 10000 })
    : json(route, { error: 'open shift not found' }, 404))
}

test('组合产品在窗口按一个商品销售并提交组合标识', async ({ page }) => {
  await preparePOS(page, true)
  let orderPayload: any
  await page.route('**/api/v1/orders', async route => {
    orderPayload = route.request().postDataJSON()
    await json(route, { id: 52, order_no: 'POS-BUNDLE-1', total_amount: 150, status: 'unpaid' }, 201)
  })

  await page.goto('/#/')
  await page.getByRole('button', { name: /双景区联票/ }).click()
  await expect(page.locator('.cart-item').getByText('双景区联票', { exact: true })).toBeVisible()
  await expect(page.locator('.cart-item')).toContainText('150.00')
  await page.getByRole('button', { name: '收款' }).click()

  await expect.poll(() => orderPayload).toMatchObject({
    channel: 'window', total_amount: 150,
    items: [{ bundle_product_id: 21, quantity: 1 }],
  })
})

async function preparePOS(page: Page, openShift: boolean, sessionStaff = staff) {
  await page.addInitScript(({ staff, openShift }) => {
    sessionStorage.setItem('token', 'staff-token')
    sessionStorage.setItem('staff', JSON.stringify(staff))
    localStorage.setItem('pos_device_id', '21')
    localStorage.setItem('pos_checkpoint_id', '31')
    if (openShift) localStorage.setItem('pos_shift_state', JSON.stringify({ isOpen: true, shiftId: 41, startTime: '2026-08-01T08:00:00Z', operator: staff.name, openingCents: 10000 }))
  }, { staff: sessionStaff, openShift })
  await mockPOSBoot(page, openShift)
}

test('员工可以登录窗口端', async ({ page }) => {
  await mockPOSBoot(page, false)
  await page.route('**/api/v1/auth/staff/login', route => json(route, { token: 'staff-token', staff }))
  await page.goto('/#/login')
  await page.getByPlaceholder('请输入系统编号').fill('SYS001')
  await page.getByPlaceholder('请输入工号').fill('SELLER001')
  await page.getByPlaceholder('请输入密码').fill('staff-password')
  await page.getByRole('button', { name: '登录窗口端' }).click()
  await expect(page.getByText('窗口售票', { exact: true })).toBeVisible()
  await expect(page.getByText('测试售票员 · SELLER001', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '核销', exact: true })).toHaveCount(0)
})

test('纯验票员工只进入核销工作区', async ({ page }) => {
  await preparePOS(page, false, checkerStaff)
  await page.goto('/#/')
  await expect(page.getByRole('heading', { name: '票券核销' })).toBeVisible()
  await expect(page.getByRole('button', { name: '核销', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '终端', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '售票', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '订单', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: /未开班|当班中/ })).toHaveCount(0)
  await page.getByRole('button', { name: '终端', exact: true }).click()
  await expect(page.getByRole('heading', { name: '窗口归属' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '当前班次' })).toHaveCount(0)
})

test('售票员可以查询并搜索启用的票务政策', async ({ page }) => {
  await preparePOS(page, true)
  await page.route('**/api/v1/policies*', route => json(route, { data: [
    { id: 1, category: 'Admission', title: '儿童免票', content: '身高一米二以下儿童免票', is_active: true },
    { id: 2, category: 'Refund', title: '退票规则', content: '未使用门票可按票规申请退款', is_active: true },
  ] }))
  await page.goto('/#/')
  await page.getByRole('button', { name: '政策', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '票务政策' })
  await expect(dialog.getByText('儿童免票', { exact: true })).toBeVisible()
  await dialog.getByPlaceholder('搜索政策关键词，例如免票、退款').fill('退款')
  await expect(dialog.getByText('退票规则', { exact: true })).toBeVisible()
  await expect(dialog.getByText('儿童免票', { exact: true })).toBeHidden()
})

test('备用金开班和交班汇总按支付方式展示', async ({ page }) => {
  await preparePOS(page, false)
  await page.route('**/api/v1/operations/shifts', async route => {
    expect(route.request().postDataJSON()).toEqual({ device_id: 21, opening_cents: 10000 })
    await json(route, { id: 41, opened_at: '2026-08-01T08:00:00Z', opening_cents: 10000 })
  })
  await page.route('**/api/v1/operations/shifts/41/summary', route => json(route, {
    shift: { id: 41, opening_cents: 10000 },
    cash_expected_cents: 12500,
    payments: [
      { method: 'cash', gross_cents: 3000, refund_cents: 500, net_cents: 2500 },
      { method: 'wechat', gross_cents: 2000, refund_cents: 0, net_cents: 2000 },
      { method: 'alipay', gross_cents: 1000, refund_cents: 200, net_cents: 800 },
    ],
  }))
  await page.route('**/api/v1/operations/shifts/41/close', async route => {
    expect(route.request().postDataJSON()).toMatchObject({ closing_cents: 12500 })
    await json(route, { status: 'closed' })
  })

  await page.goto('/#/')
  await page.getByRole('button', { name: /未开班/ }).click()
  await page.getByRole('dialog', { name: '开始当班' }).getByRole('spinbutton').fill('100')
  await page.getByRole('button', { name: '确认开班' }).click()
  await expect(page.getByRole('button', { name: /当班中/ })).toBeVisible()

  await page.getByRole('button', { name: /当班中/ }).click()
  const dialog = page.getByRole('dialog', { name: '交班清点' })
  await expect(dialog.getByText('净收 ¥25.00')).toBeVisible()
  await expect(dialog.getByText('净收 ¥20.00')).toBeVisible()
  await expect(dialog.getByText('净收 ¥8.00')).toBeVisible()
  await expect(dialog.getByText('¥125.00')).toBeVisible()
  await page.getByRole('button', { name: '确认关班' }).click()
  await expect(page.getByRole('button', { name: /未开班/ })).toBeVisible()
})

test('窗口订单使用服务端真实退款状态筛选', async ({ page }) => {
  await preparePOS(page, true)
  const orderQueries: string[] = []
  await page.route('**/api/v1/orders?*', route => {
    orderQueries.push(route.request().url())
    return json(route, { data: [{ id: 51, order_no: 'POS-REFUNDED-1', contact_name: '退款游客', total_amount: 80, status: 'refunded', items: [], created_at: '2026-08-01T09:00:00Z' }], total: 41 })
  })

  await page.goto('/#/')
  await page.getByRole('button', { name: '订单', exact: true }).click()
  await page.getByRole('combobox', { name: '订单状态' }).press('ArrowDown')
  await page.getByRole('option', { name: '已退款', exact: true }).click()

  await expect.poll(() => orderQueries.some(url => new URL(url).searchParams.get('status') === 'refunded')).toBe(true)
  const refundedRow = page.getByRole('row').filter({ hasText: 'POS-REFUNDED-1' })
  await expect(refundedRow.getByText('POS-REFUNDED-1')).toBeVisible()
  await expect(refundedRow.getByText('已退款', { exact: true })).toBeVisible()
  await expect(page.locator('.el-pagination__total')).toContainText('41')
  await page.locator('.el-pager li').filter({ hasText: '2' }).click()
  await expect.poll(() => orderQueries.some(url => new URL(url).searchParams.get('page') === '2')).toBe(true)
})

test('现金找零正确且打印未配置时保留订单与购物清单', async ({ page }) => {
  await preparePOS(page, true)
  let paymentPayload: any
  await page.route('**/api/v1/orders', route => json(route, { id: 51, order_no: 'POS-E2E-1', total_amount: 80, status: 'unpaid' }))
  await page.route('**/api/v1/payments/orders/POS-E2E-1', route => json(route, { has_partial_cash: false, payments: [] }))
  await page.route('**/api/v1/payments/pay', async route => {
    paymentPayload = route.request().postDataJSON()
    await json(route, { id: 61, status: 'paid', method: 'cash' })
  })
  await page.route('**/api/v1/operations/print-jobs', route => json(route, { id: 71, order_no: 'POS-E2E-1' }, 201))
  await page.route('**/api/v1/operations/print-jobs/71/status', route => json(route, { status: 'ok' }))

  await page.goto('/#/')
  await page.getByRole('button', { name: /标准成人票/ }).click()
  await page.getByRole('button', { name: '收款' }).click()
  const payment = page.getByRole('dialog', { name: '收款' })
  await payment.getByRole('spinbutton').fill('100')
  await expect(payment.getByText('应找零')).toBeVisible()
  await expect(payment.getByText('¥20.00')).toBeVisible()
  await payment.getByRole('button', { name: '确认现金收款' }).click()

  await expect.poll(() => paymentPayload).toMatchObject({
    order_no: 'POS-E2E-1', method: 'cash', amount_cents: 8000, cash_tendered_cents: 10000, shift_id: 41, device_id: 21,
  })
  await expect(page.getByText('支付已成功，但打印失败。订单和打印任务已保留，可稍后重打。')).toBeVisible()
  await expect(page.locator('.cart-item').getByText('标准成人票', { exact: true })).toBeVisible()
})

test('付款码入口自动识别微信并完成收款', async ({ page }) => {
  await preparePOS(page, true)
  let paymentPayload: any
  await page.route('**/api/v1/orders', route => json(route, { id: 52, order_no: 'POS-E2E-CODE', total_amount: 80, status: 'unpaid' }))
  await page.route('**/api/v1/payments/orders/POS-E2E-CODE', route => json(route, { has_partial_cash: false, payments: [] }))
  await page.route('**/api/v1/payments/pay', async route => {
    paymentPayload = route.request().postDataJSON()
    await json(route, { id: 71, order_no: 'POS-E2E-CODE', method: 'wechat', pay_type: 'bscanc', status: 'paid', amount_cents: 8000 }, 201)
  })

  await page.goto('/#/')
  await page.getByRole('button', { name: /标准成人票/ }).click()
  await page.getByRole('button', { name: '收款' }).click()
  const payment = page.getByRole('dialog', { name: '收款' })
  await payment.getByRole('button', { name: '付款码收款' }).click()
  await payment.getByPlaceholder('等待扫码').fill('100000000000000000')
  await payment.getByRole('button', { name: '确认付款码收款' }).click()

  await expect.poll(() => paymentPayload).toMatchObject({
    method: 'auto', pay_type: 'bscanc', auth_code: '100000000000000000', amount_cents: 8000,
  })
  await expect(payment).not.toBeVisible()
})
