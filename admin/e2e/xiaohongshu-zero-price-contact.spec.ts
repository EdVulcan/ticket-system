import { expect, test, type Page, type Route } from '@playwright/test'

const supplierUser = {
  id: 8, username: 'supplier_admin', role: 'super_admin', scope: 'tenant', tenant_id: 1,
  tenant_name: '青云景区', system_code: 'QY001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}
const product = {
  id: 101, name: '青云景区成人票', scenic_area_id: 11, product_kind: 'ticket',
  price: 80, settlement_price: 60, type: 'online', status: 'online', code_mode: 'order',
  validity_type: 'unlimited', validity_days: 0, validity_start_date: null, validity_end_date: null,
  stock_type: 'unlimited', daily_stock: 0, real_name_required: false, region_limit: '',
  limit_per_phone: 0, limit_per_id: 0, refund_type: 'no_refund', refund_rule: '', tags: '',
  is_distributable: false, gate_voice_code: 'welcome',
  rule: { name: '成人票规则', validity_type: 'unlimited', groups: [
    { group_name: '大门', max_total_check_in: 1, items: [{ check_point_id: 31, max_per_check_in: 1 }] },
  ] },
}
const zeroPriceNotice = '支持零元商品；订单成交和出票仍须由小红书平台确认。'

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareSupplier(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, supplierUser)
  await page.route('**/api/v1/**', route => json(route, { data: [] }))
  await page.route('**/api/v1/tenants/me', route => json(route, { ...supplierUser, id: 1, status: 'active' }))
  await page.route('**/api/v1/products?*', route => json(route, { data: [product], total: 1 }))
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [{ id: 31, name: '东门检票点', scenic_area_id: 11 }], total: 1 }))
  await page.route('**/api/v1/scenic-areas', route => json(route, { data: [{ id: 11, name: '青云景区', status: 'active' }] }))
}

test('小红书映射默认零价可添加，负数受限且携程仍需正价', async ({ page }) => {
  await prepareSupplier(page)
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [
    { id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production' },
    { id: 8, code: 'ctrip-qa', type: 'ctrip', status: 'active', environment: 'production' },
  ] }))
  const writes: any[] = []
  await page.route('**/api/v1/channel-accounts/mappings*', async route => {
    if (route.request().method() === 'POST') {
      writes.push(route.request().postDataJSON())
      await json(route, { id: 31, ...writes[writes.length - 1] })
      return
    }
    await json(route, { data: [] })
  })
  await page.goto('/channels')
  await page.getByRole('row').filter({ hasText: 'xhs-qa' }).getByRole('button', { name: '商品映射', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商品映射', exact: true })
  const price = dialog.getByPlaceholder('小红书售价')
  expect(Number(await price.inputValue())).toBe(0)
  await expect(price).toHaveAttribute('aria-valuemin', '0')
  await expect(dialog.getByText(zeroPriceNotice, { exact: true })).toBeVisible()
  await price.fill('-2')
  await price.press('Tab')
  expect(Number(await price.inputValue())).toBeGreaterThanOrEqual(0)
  await dialog.getByPlaceholder('外部商品编码 / PLU').fill('ZERO-TICKET')
  await dialog.getByRole('combobox').click()
  await page.getByRole('option', { name: product.name }).click()
  await price.fill('0')
  await dialog.getByRole('button', { name: '添加', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].channel_sale_cents).toBe(0)
  await dialog.locator('.el-dialog__headerbtn').click()
  await page.getByRole('row').filter({ hasText: 'ctrip-qa' }).getByRole('button', { name: '商品映射', exact: true }).click()
  const ctripPrice = dialog.getByPlaceholder('携程销售价')
  await expect(ctripPrice).toHaveAttribute('aria-valuemin', '0.01')
  await expect(dialog.getByText(zeroPriceNotice, { exact: true })).toHaveCount(0)
})

test('小红书已保存零价打开后仍为零，发布保存不回退为一分钱', async ({ page }) => {
  await prepareSupplier(page)
  const mapping = { id: 31, external_code: 'ZERO-TICKET', product_id: 101, channel_sale_cents: 0, channel_cost_cents: 0, status: 'active' }
  const config = {
    external_sku_id: 'ZERO-SKU', category_id: 'travel', poi_ids: ['poi-1'],
    image_url: 'https://example.test/ticket.png', description: '零元测试门票',
    product_path: '/pages/product/detail', order_path: '/pages/order/detail', product_type: 1, settle_type: 1,
  }
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [{ id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active' }] }))
  await page.route('**/api/v1/channel-accounts/mappings?*', route => json(route, { data: [mapping] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-categories', route => json(route, { data: [{ category_id: 'travel', name: '旅游', support_trade: true }] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-pois?*', route => json(route, { data: [{ poi_id: 'poi-1', name: '青云景区' }] }))
  const writes: any[] = []
  await page.route('**/api/v1/channel-accounts/7/mappings/31', async route => {
    writes.push(route.request().postDataJSON())
    await json(route, { updated: true })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', route => json(route, config))
  await page.goto('/channels')
  await page.getByRole('button', { name: '商品映射', exact: true }).click()
  await page.getByRole('button', { name: '发布配置', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '小红书商品发布配置', exact: true })
  const price = dialog.getByRole('spinbutton', { name: '小红书售价', exact: true })
  expect(Number(await price.inputValue())).toBe(0)
  await expect(dialog.getByText(zeroPriceNotice, { exact: true })).toBeVisible()
  await price.fill('80')
  await price.press('Tab')
  await expect(dialog.getByText(zeroPriceNotice, { exact: true })).toHaveCount(0)
  await price.fill('0')
  await dialog.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].channel_sale_cents).toBe(0)
  expect(Number(await price.inputValue())).toBe(0)
})

test('联系人实名开关兼容旧产品并独立保存，新增产品默认关闭', async ({ page }) => {
  await prepareSupplier(page)
  let currentProduct = { ...product, contact_real_name_required: undefined as boolean | undefined }
  await page.route('**/api/v1/products?*', route => json(route, { data: [currentProduct], total: 1 }))
  const writes: any[] = []
  await page.route('**/api/v1/products/101', async route => {
    const submitted = route.request().postDataJSON()
    writes.push(submitted)
    currentProduct = { ...currentProduct, ...submitted.product }
    await json(route, currentProduct)
  })
  await page.goto('/product')
  await page.getByRole('button', { name: '编辑', exact: true }).click()
  let dialog = page.getByRole('dialog', { name: '编辑门票', exact: true })
  await dialog.getByRole('tab', { name: '购买限制' }).click()
  let toggle = dialog.getByRole('switch', { name: '订单联系人实名（小红书）', exact: true })
  await expect(toggle).not.toBeChecked()
  await expect(dialog.getByText('仅适用于小红书：', { exact: false })).toBeVisible()
  await dialog.getByText('订单联系人实名（小红书）', { exact: true }).click()
  await expect(toggle).toBeChecked()
  await dialog.getByRole('button', { name: '保存并发布', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].product.contact_real_name_required).toBe(true)
  expect(writes[0].product.real_name_required).toBe(false)
  await page.getByRole('button', { name: '编辑', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑门票', exact: true })
  await dialog.getByRole('tab', { name: '购买限制' }).click()
  toggle = dialog.getByRole('switch', { name: '订单联系人实名（小红书）', exact: true })
  await expect(toggle).toBeChecked()
  await dialog.getByText('订单联系人实名（小红书）', { exact: true }).click()
  await expect(toggle).not.toBeChecked()
  await dialog.getByRole('button', { name: '保存并发布', exact: true }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1].product.contact_real_name_required).toBe(false)
  expect(writes[1].product.real_name_required).toBe(false)
  await page.getByRole('button', { name: '发布新门票', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '发布新门票', exact: true })
  await dialog.getByRole('tab', { name: '购买限制' }).click()
  await expect(dialog.getByRole('switch', { name: '订单联系人实名（小红书）', exact: true })).not.toBeChecked()
})
