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
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}

const account = { id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production', protocol_configured: true }
const mapping = { id: 31, external_code: 'XHS-ADULT', display_name: '小红书成人票', product_id: 101, channel_sale_cents: 9900, channel_cost_cents: 0, status: 'active' }

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareChannel(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, supplierUser)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: supplierUser.tenant_id,
    name: supplierUser.tenant_name,
    system_code: supplierUser.system_code,
    status: 'active',
    capabilities: supplierUser.capabilities,
    supplier_business_types: supplierUser.supplier_business_types,
  }))
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [account] }))
  await page.route('**/api/v1/channel-accounts/mappings?*', route => json(route, { data: [mapping] }))
  await page.route('**/api/v1/products?*', route => json(route, { data: [{ id: 101, name: '青云景区成人票' }] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-categories', route => json(route, { data: [{ category_id: 'travel', name: '旅游', support_trade: true }] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-pois?*', route => json(route, { data: [{ poi_id: 'poi-1', name: '青云景区' }] }))
}

test('小红书审核手动查询仅更新审核投影并保留未保存商品字段', async ({ page }) => {
  await prepareChannel(page)
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', route => json(route, {
    external_sku_id: 'XHS-ADULT-SKU', category_id: 'travel', poi_ids: ['poi-1'], image_url: 'https://example.test/adult.png', description: '已保存的商品说明', product_path: '/pages/product', order_path: '/pages/order', product_type: 1, settle_type: 1,
    sync_status: 'submitted', audit_status: 'pending', audit_message: '', audited_at: '', audit_checked_at: '2026-09-07T08:00:00Z', audit_check_error: '',
  }))
  let refreshCalls = 0
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-audit-refresh', async route => {
    expect(route.request().method()).toBe('POST')
    refreshCalls += 1
    await json(route, {
      external_sku_id: 'SERVER-SKU-MUST-NOT-REPLACE-DRAFT', description: '服务端旧说明不得覆盖草稿',
      sync_status: 'synced', audit_status: 'pending', audit_message: '平台仍在处理中', audited_at: '',
      audit_checked_at: '2026-09-07T08:05:00Z', audit_check_error: '上游暂未返回明确审核结论',
    })
  })

  await page.goto('/channels')
  await page.getByRole('button', { name: '商品映射' }).click()
  await page.getByRole('button', { name: '发布配置' }).click()
  const dialog = page.getByRole('dialog', { name: '小红书商品发布配置' })
  const description = dialog.getByRole('textbox', { name: '商品说明' })
  await expect(description).toHaveValue('已保存的商品说明')
  await description.fill('运营人员尚未保存的商品说明')

  await dialog.getByRole('button', { name: '立即查询审核状态' }).click()
  await expect.poll(() => refreshCalls).toBe(1)
  await expect(description).toHaveValue('运营人员尚未保存的商品说明')
  await expect(dialog.getByText('审核状态尚未明确，商品暂不可售')).toBeVisible()
  await expect(dialog.getByText('平台仍在处理中')).toBeVisible()
  await expect(dialog.getByText('上次查询：')).toContainText('2026')
  await expect(dialog.getByText('查询异常：上游暂未返回明确审核结论')).toBeVisible()
})

test('小红书审核查询失败保留当前草稿并显示查询错误', async ({ page }) => {
  await prepareChannel(page)
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', route => json(route, {
    external_sku_id: 'XHS-ADULT-SKU', category_id: 'travel', poi_ids: ['poi-1'], image_url: 'https://example.test/adult.png', description: '已保存的商品说明', product_path: '/pages/product', order_path: '/pages/order', product_type: 1, settle_type: 1,
    sync_status: 'submitted', audit_status: 'pending', audit_message: '', audited_at: '', audit_checked_at: '', audit_check_error: '',
  }))
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-audit-refresh', route => json(route, { error: '小红书审核查询失败' }, 502))

  await page.goto('/channels')
  await page.getByRole('button', { name: '商品映射' }).click()
  await page.getByRole('button', { name: '发布配置' }).click()
  const dialog = page.getByRole('dialog', { name: '小红书商品发布配置' })
  const description = dialog.getByRole('textbox', { name: '商品说明' })
  await description.fill('查询失败时也必须保留的草稿')

  await dialog.getByRole('button', { name: '立即查询审核状态' }).click()
  await expect(description).toHaveValue('查询失败时也必须保留的草稿')
  await expect(dialog.getByText('查询异常：小红书审核查询失败')).toBeVisible()
  await expect(dialog.getByText('审核状态尚未明确，商品暂不可售')).toBeVisible()
})

test('小红书审核本地轮询显示通过结果，关闭配置框后停止轮询', async ({ page }) => {
  await page.clock.install()
  await prepareChannel(page)
  let localProjectionReads = 0
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', route => {
    localProjectionReads += 1
    return json(route, {
      external_sku_id: 'XHS-ADULT-SKU', category_id: 'travel', poi_ids: ['poi-1'], image_url: 'https://example.test/adult.png', description: '已保存的商品说明', product_path: '/pages/product', order_path: '/pages/order', product_type: 1, settle_type: 1,
      sync_status: 'synced', audit_status: 'approved', audit_message: '小红书已确认通过', audited_at: '2026-09-07T08:04:00Z', audit_checked_at: '2026-09-07T08:05:00Z', audit_check_error: '',
    })
  })

  await page.goto('/channels')
  await page.getByRole('button', { name: '商品映射' }).click()
  await page.getByRole('button', { name: '发布配置' }).click()
  const dialog = page.getByRole('dialog', { name: '小红书商品发布配置' })
  await expect(dialog.getByText('审核已通过')).toBeVisible()
  await expect(dialog.getByText('小红书已确认通过')).toBeVisible()
  await expect.poll(() => localProjectionReads).toBe(1)

  await page.clock.fastForward(30_000)
  await expect.poll(() => localProjectionReads).toBe(2)
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await page.clock.fastForward(60_000)
  await expect.poll(() => localProjectionReads).toBe(2)
})
