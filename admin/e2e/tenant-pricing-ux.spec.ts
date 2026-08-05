import { expect, test, type Page, type Route } from '@playwright/test'

const supplierUser = {
  id: 8,
  username: 'supplier_admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '青云景区',
  system_code: 'QY001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareSupplier(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, supplierUser)
  await page.route('**/api/v1/teams?page=*', route => json(route, { data: [], total: 0 }))
  await page.route('**/api/v1/teams/contracts', async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      expect(body.travel_tenant_id).toBe(21)
      expect(body.price_rules).toEqual([{ product_id: 101, price_cents: 8800, max_quantity: 50 }])
      await json(route, { id: 31, ...body }, 201)
      return
    }
    await json(route, { data: [] })
  })
  await page.route('**/api/v1/teams/contract-partners', route => json(route, { data: [
    { tenant_id: 21, name: '山河旅行社', system_code: 'SHLX', relationship_id: 6 },
  ] }))
  await page.route('**/api/v1/products?*', route => json(route, { data: [
    { id: 101, name: '青云景区团队票', status: 'online', is_distributable: true },
  ] }))
}

test('供应商按旅行社和产品名称维护合同结算价', async ({ page }) => {
  await prepareSupplier(page)
  await page.goto('/teams')
  await page.getByRole('tab', { name: '旅行社合同' }).click()
  await page.getByRole('button', { name: '新增旅行社合同' }).click()

  const dialog = page.getByRole('dialog', { name: '新增旅行社合同' })
  await dialog.getByRole('combobox', { name: /旅行社/ }).click()
  await page.getByRole('option', { name: /山河旅行社/ }).click()
  await dialog.getByRole('textbox', { name: /合同号/ }).fill('QY-TEAM-2026')
  await dialog.getByRole('combobox', { name: '产品' }).click()
  await page.getByRole('option', { name: '青云景区团队票' }).click()
  await dialog.getByRole('spinbutton', { name: '结算价（元）' }).fill('88')
  await dialog.getByRole('spinbutton', { name: '每单上限（0不限）' }).fill('50')
  await expect(dialog.getByText('保存后同时作为该旅行社的供货结算价')).toBeVisible()
  await dialog.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('旅行社合同已创建')).toBeVisible()
})

test('供应商可以为票种配置闸机本地语音编号', async ({ page }) => {
  await prepareSupplier(page)
  const product = {
    id: 101, name: '青云景区成人票', price: 80, settlement_price: 60,
    type: 'online', status: 'online', code_mode: 'ticket', validity_type: 'date', validity_days: 0,
    stock_type: 'unlimited', daily_stock: 0, real_name_required: false, region_limit: '',
    limit_per_phone: 0, limit_per_id: 0, refund_type: 'no_refund', refund_rule: '', tags: '',
    is_distributable: true, gate_voice_code: 'adult_ticket',
    rule: { name: '成人票规则', validity_type: 'date', groups: [
      { group_name: '大门', max_total_check_in: 1, items: [{ check_point_id: 31, max_per_check_in: 1 }] },
    ] },
  }
  await page.route('**/api/v1/products?*', route => json(route, { data: [product], total: 1 }))
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [{ id: 31, name: '东门检票点' }], total: 1 }))
  let submitted: any
  await page.route('**/api/v1/products/101', async route => {
    submitted = route.request().postDataJSON()
    await json(route, { ...product, ...submitted.product })
  })

  await page.goto('/product')
  await page.getByRole('button', { name: '编辑' }).click()
  const dialog = page.getByRole('dialog', { name: '编辑门票' })
  const voice = dialog.locator('.el-form-item').filter({ hasText: '闸机本地语音' }).getByRole('combobox')
  await voice.fill('vip_ticket')
  await voice.press('Enter')
  await dialog.getByRole('button', { name: '保存并发布' }).click()
  await expect.poll(() => submitted?.product?.gate_voice_code).toBe('vip_ticket')
})

test('支付配置页展示各渠道真实接入状态', async ({ page }) => {
  await prepareSupplier(page)
  let submittedPaymentConfig = ''
  await page.route('**/api/v1/payments/configs', route => json(route, { data: [
    { provider: 'wechat', app_id: 'wx-app', mch_id: 'merchant', key: '******', wechat_v2_key: '******', private_key: '******', merchant_certificate: '******', public_key: '', serial_no: 'serial', platform_public_key: '', platform_public_key_id: '', status: false },
  ] }))
  await page.route('**/api/v1/payments/configs/readiness', route => json(route, { data: [
    { provider: 'wechat', name: '微信支付', configured: true, enabled: false, configuration_ready: false, issues: ['缺少微信支付平台公钥'], capabilities: [
      { code: 'customer_scan', name: '顾客扫码支付', available: false },
      { code: 'payment_code', name: '付款码收款', available: true, note: '已具备配置，仍需真实商户小额联调' },
    ] },
    { provider: 'alipay', name: '支付宝', configured: false, enabled: false, configuration_ready: false, issues: ['尚未保存配置'], capabilities: [] },
  ] }))
  await page.route('**/api/v1/payments/configs/wechat', route => {
    submittedPaymentConfig = route.request().postData() || ''
    return json(route, { provider: 'wechat', status: false })
  })

  await page.goto('/payment-config')
  await expect(page.getByText('尚未启用').first()).toBeVisible()
  await expect(page.getByText('缺少微信支付平台公钥')).toBeVisible()
  await expect(page.getByText('付款码收款', { exact: true })).toBeVisible()
  await expect(page.getByText('已具备配置，仍需真实商户小额联调')).toBeVisible()
  await expect(page.getByLabel('第二版接口密钥（付款码收款，32位）')).toHaveValue('******')
  await expect(page.getByLabel('支付结果通知地址')).toHaveCount(0)
  await page.getByRole('button', { name: '保存微信配置' }).click()
  await expect.poll(() => submittedPaymentConfig).not.toContain('notify_url')
})
