import { expect, test } from '@playwright/test'

test('商品映射默认线上，可切换线下且不影响已有映射', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
  const user = { id: 8, role: 'super_admin', scope: 'tenant', tenant_id: 1,
    capabilities: [{ capability: 'supplier', status: 'active' }],
    supplier_business_types: [{ business_type: 'scenic', status: 'active' }] }
  await page.addInitScript(value => {
    localStorage.setItem('token', 'test-token')
    localStorage.setItem('user', JSON.stringify(value))
  }, user)
  const products = [
    { id: 11, name: '线上成人票', type: 'online', product_kind: 'ticket', status: 'online' },
    { id: 12, name: '窗口成人票', type: 'offline', product_kind: 'ticket', status: 'online' },
    { id: 13, name: '独立酒店房型', type: 'online', product_kind: 'hotel', status: 'online' },
  ]
  const writes: any[] = []
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let body: unknown = { data: [] }
    if (path.endsWith('/tenants/me')) body = { ...user, id: 1, status: 'active' }
    if (path.endsWith('/channel-accounts')) body = { data: [{ id: 7, code: 'xhs-qa', type: 'xiaohongshu', status: 'active', environment: 'production' }] }
    if (path.endsWith('/products')) body = { data: products, total: products.length }
    if (path.endsWith('/channel-accounts/mappings')) {
      body = { data: [{ id: 9, product_id: 12, external_code: 'EXISTING', status: 'active' }] }
      if (route.request().method() === 'POST') {
        writes.push(route.request().postDataJSON())
        body = { id: 10, ...writes[0] }
      }
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/channels')
  await expect(page).toHaveURL(/\/channels$/)
  await expect(page).toHaveTitle('景区票务管理系统')
  await page.getByRole('button', { name: '商品映射', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商品映射', exact: true })
  const selector = dialog.getByRole('combobox')
  await expect(dialog.getByRole('radio', { name: '线上', exact: true })).toBeChecked()
  await expect(dialog.getByText('窗口成人票', { exact: true })).toBeVisible()
  await selector.click()
  await expect(page.getByRole('option', { name: '窗口成人票', exact: true })).toHaveCount(0)
  await expect(page.getByRole('option', { name: '独立酒店房型' })).toHaveCount(0)
  await page.getByRole('option', { name: '线上成人票', exact: true }).click()
  await expect(dialog.locator('.el-select')).toContainText('线上成人票')
  await dialog.getByText('线下（窗口）', { exact: true }).click()
  await expect(selector).toHaveValue('')
  await expect(dialog.locator('.el-select')).toContainText('选择本商户产品')
  expect(writes).toHaveLength(0)
  await selector.click()
  await expect(page.getByRole('option', { name: '线上成人票', exact: true })).toHaveCount(0)
  await page.getByRole('option', { name: '窗口成人票', exact: true }).click()
  await dialog.getByPlaceholder('外部商品编码 / PLU').fill('NEW-OFFLINE')
  await dialog.getByPlaceholder('小红书售价').fill('80')
  await dialog.getByRole('button', { name: '添加', exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ channel_account_id: 7, product_id: 12, external_code: 'NEW-OFFLINE', channel_sale_cents: 8000 })
  await dialog.locator('.el-dialog__headerbtn').click()
  await page.getByRole('button', { name: '商品映射', exact: true }).click()
  await expect(dialog.getByRole('radio', { name: '线上', exact: true })).toBeChecked()
  await expect(selector).toHaveValue('')
  await expect(page.locator('vite-error-overlay')).toHaveCount(0)
  await page.screenshot({ path: `${process.env.TEMP || '/tmp'}/channel-product-filter.png`, animations: 'disabled' })
  expect(errors).toEqual([])
})
