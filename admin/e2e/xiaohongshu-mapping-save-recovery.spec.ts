import { expect, test, type Page, type Route } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

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
const savedConfig = {
  external_sku_id: 'XHS-ADULT-SKU',
  category_id: 'travel',
  poi_ids: ['poi-1'],
  image_url: 'https://example.test/adult.png',
  description: '已保存的商品说明',
  product_path: '/pages/product',
  order_path: '/pages/order',
  product_type: 1,
  settle_type: 1,
  sync_status: 'synced',
  audit_status: 'approved',
  audit_message: '审核通过',
  audited_at: '2026-09-07T08:00:00Z',
  audit_checked_at: '2026-09-07T08:05:00Z',
  audit_check_error: '',
  last_sync_error: '',
}

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
  await page.route('**/api/v1/products?*', route => json(route, { data: [{ id: 101, name: '青云景区成人票' }] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-categories', route => json(route, { data: [{ category_id: 'travel', name: '旅游', support_trade: true }] }))
  await page.route('**/api/v1/channel-accounts/7/xiaohongshu-pois?*', route => json(route, { data: [{ poi_id: 'poi-1', name: '青云景区' }] }))
}

async function openMapping(page: Page) {
  await page.goto('/channels')
  await page.getByRole('button', { name: '商品映射' }).click()
  await page.getByRole('button', { name: '发布配置' }).click()
  return page.getByRole('dialog', { name: '小红书商品发布配置' })
}

test('发布配置失败时显示映射已保存并恢复服务端配置', async ({ page }) => {
  await prepareChannel(page)
  let mappingReads = 0
  let configReads = 0
  await page.route('**/api/v1/channel-accounts/mappings?*', route => {
    mappingReads += 1
    return json(route, { data: [mappingReads === 1 ? mapping : { ...mapping, external_code: 'XHS-UPDATED' }] })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31', async route => {
    expect(route.request().method()).toBe('PATCH')
    await json(route, { updated: true })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', async route => {
    if (route.request().method() === 'GET') {
      configReads += 1
      await json(route, configReads === 1 ? savedConfig : { ...savedConfig, description: '服务端旧说明', sync_status: 'pending', audit_status: 'pending', audit_message: '', audited_at: '' })
      return
    }
    expect(route.request().method()).toBe('PUT')
    await json(route, { error: '发布参数校验失败' }, 422)
  })

  const dialog = await openMapping(page)
  await dialog.getByRole('textbox', { name: '外部商品编码' }).fill('XHS-UPDATED')
  await dialog.getByRole('textbox', { name: '商品说明' }).fill('尚未保存的商品说明')
  await dialog.getByRole('button', { name: '保存配置', exact: true }).click()

  await expect(dialog.getByText('映射已保存，发布配置未保存', { exact: false })).toBeVisible()
  await expect(dialog.getByRole('textbox', { name: '外部商品编码' })).toHaveValue('XHS-UPDATED')
  await expect(dialog.getByRole('textbox', { name: '商品说明' })).toHaveValue('服务端旧说明')
  await expect(dialog.getByText('配置已保存，尚未同步商品')).toBeVisible()
  await expect.poll(() => mappingReads).toBe(2)
  await expect.poll(() => configReads).toBe(2)
})

test('同步失败时保留已保存配置并显示同步失败结果', async ({ page }) => {
  await prepareChannel(page)
  let mappingReads = 0
  let configReads = 0
  await page.route('**/api/v1/channel-accounts/mappings?*', route => {
    mappingReads += 1
    return json(route, { data: [mappingReads === 1 ? mapping : { ...mapping, external_code: 'XHS-SYNC-EDIT' }] })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31', async route => {
    expect(route.request().method()).toBe('PATCH')
    await json(route, { updated: true })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', async route => {
    if (route.request().method() === 'GET') {
      configReads += 1
      await json(route, configReads === 1 ? savedConfig : { ...savedConfig, description: '同步前已保存的说明', sync_status: 'failed', audit_status: 'pending', audit_message: '', audited_at: '', last_sync_error: '平台同步失败' })
      return
    }
    expect(route.request().method()).toBe('PUT')
    await json(route, { ...savedConfig, sync_status: 'pending', audit_status: 'pending', audit_message: '', audited_at: '', last_sync_error: '' })
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-sync', async route => {
    expect(route.request().method()).toBe('POST')
    await json(route, { error: '平台同步失败' }, 502)
  })

  const dialog = await openMapping(page)
  await dialog.getByRole('textbox', { name: '外部商品编码' }).fill('XHS-SYNC-EDIT')
  await dialog.getByRole('textbox', { name: '商品说明' }).fill('同步前已保存的说明')
  await dialog.getByRole('button', { name: '保存并同步', exact: true }).click()

  await expect(dialog.getByText('映射和发布配置已保存，但商品同步失败', { exact: false })).toBeVisible()
  await expect(dialog.getByText('平台同步失败', { exact: true })).toBeVisible()
  await expect(dialog.getByText('商品同步失败')).toBeVisible()
  await expect(dialog.getByRole('textbox', { name: '外部商品编码' })).toHaveValue('XHS-SYNC-EDIT')
  await expect.poll(() => mappingReads).toBe(2)
  await expect.poll(() => configReads).toBe(2)
})

test('映射已保存但响应丢失时显示实际结果且不继续发布', async ({ page }) => {
  await prepareChannel(page)
  let currentMapping = { ...mapping }
  let configWrites = 0
  await page.route('**/api/v1/channel-accounts/mappings?*', route => json(route, { data: [currentMapping] }))
  await page.route('**/api/v1/channel-accounts/7/mappings/31', async route => {
    currentMapping = { ...currentMapping, ...route.request().postDataJSON() }
    await route.abort('connectionreset')
  })
  await page.route('**/api/v1/channel-accounts/7/mappings/31/xiaohongshu-product', async route => {
    if (route.request().method() !== 'GET') configWrites += 1
    await json(route, savedConfig)
  })
  const dialog = await openMapping(page)
  await dialog.getByRole('textbox', { name: '外部商品编码' }).fill('XHS-RESPONSE-LOST')
  await dialog.getByRole('button', { name: '保存并同步', exact: true }).click()
  await expect(dialog.getByText('映射已保存，发布配置未执行', { exact: false })).toBeVisible()
  await expect(dialog.getByRole('textbox', { name: '外部商品编码' })).toHaveValue('XHS-RESPONSE-LOST')
  expect(configWrites).toBe(0)
  await expect(page).toHaveURL(/\/channels$/)
  await expect(page.locator('vite-error-overlay')).toHaveCount(0)
  await page.screenshot({ path: join(tmpdir(), 'ticket-system-xhs-save-recovery.png'), fullPage: false })
})
