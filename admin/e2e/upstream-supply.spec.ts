import { test, expect, type Page } from '@playwright/test'

const user = { id: 8, role: 'super_admin', scope: 'tenant', tenant_id: 1, is_initial_admin: true,
  capabilities: [{ capability: 'supplier', status: 'active' }], supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
async function identity(page: Page) {
  await page.addInitScript(value => { localStorage.setItem('token', 'test-token'); localStorage.setItem('user', JSON.stringify(value)) }, user)
}

test('供应设置启用和关闭只提交供应字段，共用连接可管理', async ({ page }) => {
  await identity(page)
  const saved: any[] = []
  const errors: string[] = []; page.on('pageerror', e => errors.push(e.message))
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let body: any = { data: [] }
    if (path === '/tenants/me') body = { ...user, id: user.tenant_id, status: 'active' }
    if (path === '/products') body = { data: [{ id: 1, name: '联游门票', type: 'online', product_kind: 'ticket', price: 80, status: 'online', scenic_area_id: 1 }], total: 1 }
    if (path === '/upstream-connections') body = { data: [{ id: 3, name: '景区智游宝', status: 'active', endpoint: 'https://supplier.example/api', corp_code: 'CORP', username: 'merchant', environment: 'production', credentials_configured: true }] }
    if (path === '/products/1/supply') {
      if (route.request().method() === 'PUT') saved.push(route.request().postDataJSON())
      body = saved.at(-1) || { enabled: false, upstream_connection_id: 3, external_product_code: 'GOODS' }
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/product')
  await page.getByRole('button', { name: '供应', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '上游供应商配置' })
  await expect(dialog.getByText('供应商商品编码', { exact: true })).toHaveCount(0)
  await dialog.getByText('外部供应商出票', { exact: true }).click()
  await dialog.getByRole('button', { name: '管理此共用连接' }).click()
  const connection = page.getByRole('dialog', { name: '智游宝共用连接' })
  await expect(connection.getByPlaceholder('留空保留已保存密钥')).toHaveValue('')
  await connection.getByRole('button', { name: '取消', exact: true }).click()
  await dialog.getByRole('button', { name: '保存出票设置' }).click()
  await expect.poll(() => saved).toEqual([{ enabled: true, upstream_connection_id: 3, external_product_code: 'GOODS' }])
  await page.getByRole('button', { name: '供应', exact: true }).click()
  await dialog.getByText('本系统出票', { exact: true }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: `${process.env.TEMP}/zyb-supply-local.png` })
  await dialog.getByRole('button', { name: '保存出票设置' }).click()
  await expect.poll(() => saved.at(-1)?.enabled).toBe(false)
  expect(errors).toEqual([])
})

async function recoveryPage(page: Page, row: () => Record<string, unknown>, action: (path: string, body: any) => number | void) {
  await identity(page)
  const order = { id: 1, tenant_id: 1, order_no: 'RECOVERY-ORDER', channel: 'xiaohongshu', environment: 'production', status: 'paid', has_upstream_supply: true, total_amount: 80, items: [] }
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let body: any = { data: [] }; let status = 200
    if (path === '/tenants/me') body = { ...user, id: 1, status: 'active' }
    if (path === '/orders') body = { data: [order], total: 1 }
    if (path === '/orders/RECOVERY-ORDER') body = { order, fulfillments: [], refunds: [] }
    if (path === '/orders/RECOVERY-ORDER/upstream') body = { data: [{ product_name: '联游门票', external_product_code: 'G', ...row() }] }
    if (route.request().method() === 'POST') {
      status = action(path, route.request().postDataJSON()) || 200
      body = status === 200 ? { status: 'pending' } : { error: '供应商暂时不可达' }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/online-order')
  await page.getByRole('button', { name: '详情', exact: true }).click()
  await expect(page.getByText('智游宝供票', { exact: true })).toBeVisible()
}

test('供应商查询失败仍刷新本地退款恢复入口', async ({ page }) => {
  let stage = 0; const submitted: any[] = []; const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  await recoveryPage(page, () => ({ issue_status: 'ready', refund_id: 42, can_recover_funding: stage === 1 }), (path, body) => {
    if (path.endsWith('/refresh')) { stage = 1; return 503 }
    if (path === '/payments/refunds/upstream-recover') { submitted.push(body); stage = 2 }
  })
  await expect(page.getByRole('button', { name: '恢复款项退款', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '查询供应商最新状态' }).click()
  await expect(page.getByText('供应商暂时不可达', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '恢复款项退款', exact: true }).click()
  const prompt = page.getByRole('dialog', { name: '恢复款项退款' })
  await prompt.getByRole('textbox').fill('核对平台失败结果后恢复')
  await prompt.getByRole('button', { name: '确认恢复退款' }).click()
  await expect.poll(() => submitted.length).toBe(1)
  expect(submitted[0]).toEqual({ refund_id: 42, reason: '核对平台失败结果后恢复' })
  await expect(page.getByRole('button', { name: '恢复款项退款', exact: true })).toHaveCount(0)
  await page.screenshot({ path: `${process.env.TEMP}/zyb-funding-recovery.png` })
  expect(errors).toEqual([])
})

test('出票恢复必须填写核对说明且沿用原订单', async ({ page }) => {
  const submitted: any[] = []
  await recoveryPage(page, () => ({ issue_status: 'pending', can_recover_issuance: submitted.length === 0, last_error: '上次请求失败' }), (path, body) => {
    if (path.endsWith('/recover-issuance')) submitted.push({ path, ...body })
  })
  await page.getByRole('button', { name: '查单并恢复出票', exact: true }).click()
  const prompt = page.getByRole('dialog', { name: '确认恢复出票' })
  await prompt.getByRole('button', { name: '确认查单并恢复' }).click()
  expect(submitted).toHaveLength(0)
  await prompt.getByRole('textbox').fill('已核对供方后台无此订单')
  await prompt.getByRole('button', { name: '确认查单并恢复' }).click()
  await expect.poll(() => submitted.length).toBe(1)
  expect(submitted[0]).toMatchObject({ path: '/orders/RECOVERY-ORDER/upstream/recover-issuance', confirmed_no_order: true })
})

test('窗口票不展示上游供应入口', async ({ page }) => {
  await identity(page)
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    const body = path === '/tenants/me' ? { ...user, id: 1, status: 'active' } : path === '/products'
      ? { data: [{ id: 1, name: '窗口门票', type: 'offline', product_kind: 'ticket', price: 80, status: 'online', scenic_area_id: 1 }], total: 1 } : { data: [] }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/product/offline')
  await expect(page.getByRole('heading', { name: '窗口门票管理' })).toBeVisible()
  await expect(page.getByRole('button', { name: '供应', exact: true })).toHaveCount(0)
})

test('上游状态冲突必须二次确认，原退款请求携带明确授权', async ({ page }) => {
  await identity(page)
  const order = { id: 1, tenant_id: 1, order_no: 'UPSTREAM-ORDER', channel: 'xiaohongshu', environment: 'production', status: 'paid', has_upstream_supply: true, total_amount: 80, items: [{ id: 1, refund_type: 'free', product_name: '联游门票', quantity: 1, tickets: [{ ticket_code: 'SHARED-CODE', status: 'unused', check_in_count: 0 }] }] }
  const posted: any[] = []
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let body: any = { data: [] }
    if (path === '/tenants/me') body = { ...user, id: user.tenant_id, status: 'active' }
    if (path === '/orders') body = { data: [order], total: 1 }
    if (path === '/orders/UPSTREAM-ORDER') body = { order, fulfillments: [], refunds: [] }
    if (path === '/payments/refunds/upstream-check') body = { requires_confirmation: true, message: '智游宝已使用，是否仍然退款？' }
    if (path === '/payments/refunds/mixed') { posted.push(route.request().postDataJSON()); body = { status: 'pending' } }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/online-order')
  await page.getByRole('button', { name: '退款', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '申请原路退款' })
  await dialog.getByPlaceholder('请填写游客申请退票的原因').fill('游客行程取消')
  await dialog.getByRole('button', { name: '确认申请退款' }).click()
  await expect(page.getByText('智游宝已使用，是否仍然退款？')).toBeVisible()
  expect(posted).toHaveLength(0)
  await page.screenshot({ path: `${process.env.TEMP}/zyb-refund-confirmation.png` })
  await page.getByRole('button', { name: '确认仍然退款', exact: true }).click()
  await expect.poll(() => posted.length).toBe(1)
  expect(posted[0]).toMatchObject({ order_no: 'UPSTREAM-ORDER', confirm_upstream_refund: true, amount: 80, ticket_codes: ['SHARED-CODE'] })
})
