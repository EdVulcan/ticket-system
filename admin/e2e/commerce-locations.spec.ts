import { expect, test, type Page, type Route } from '@playwright/test'

type Domain = 'retail' | 'restaurant'
type Location = { id: number; business_type: Domain; name: string; location_type: string; status: string }
type Write = { method: string; path: string; body: Record<string, unknown> }

const adminUser = {
  id: 21, username: 'location-admin', role: 'admin', scope: 'tenant', tenant_id: 21,
  tenant_name: '地点编辑测试商户', system_code: 'LOCATION001', capabilities: [],
  permissions: ['catalog.read', 'catalog.write', 'orders.read'],
  business_capabilities: [{ business_type: 'restaurant', status: 'active' }, { business_type: 'retail', status: 'active' }],
}
const nouns = { retail: '仓库/发货点', restaurant: '门店/取餐点' }
const tabs = { retail: '仓库与发货', restaurant: '门店与取餐' }
const pageErrors = new WeakMap<Page, string[]>()

test.beforeEach(({ page }) => {
  const errors: string[] = []
  pageErrors.set(page, errors)
  page.on('pageerror', error => errors.push(error.message))
})
test.afterEach(({ page }) => expect(pageErrors.get(page)).toEqual([]))

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

function deferred() {
  let release!: () => void
  const promise = new Promise<void>(resolve => { release = resolve })
  return { promise, release }
}

async function setup(page: Page, options: { readOnly?: boolean; failFirst?: boolean; waitForPut?: Promise<void> } = {}) {
  let identity = options.readOnly ? { ...adminUser, role: 'viewer', permissions: ['catalog.read', 'orders.read'] } : adminUser
  const rows: Location[] = [
    { id: 41, business_type: 'retail', name: '瑞遇-二两兔', location_type: 'store', status: 'active' },
    { id: 71, business_type: 'restaurant', name: '一号门店', location_type: 'pickup', status: 'inactive' },
  ]
  const writes: Write[] = []
  await page.addInitScript(user => {
    localStorage.setItem('token', 'location-test-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, identity)
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^.*\/api\/v1/, '')
    if (path === '/tenants/me') return json(route, { ...identity, id: identity.tenant_id, name: identity.tenant_name, status: 'active' })
    if (path === '/commerce/locations' && request.method() === 'GET') {
      return json(route, { data: rows.filter(row => row.business_type === url.searchParams.get('business_type')) })
    }
    if (path === '/commerce/products') return json(route, { data: [{ id: 11, name: '测试商品', status: 'online', skus: [{ id: 12, sku_code: 'SKU12', name: '标准装', status: 'active', price_cents: 1000 }] }] })
    if (path === '/commerce/inventory') return json(route, { data: [] })
    if (path === '/commerce/locations' || /^\/commerce\/locations\/\d+$/.test(path)) {
      const body = request.postDataJSON()
      writes.push({ method: request.method(), path, body })
      if (options.failFirst && writes.length === 1) return json(route, { error: '地点保存暂不可用' }, 503)
      if (request.method() === 'PUT') {
        const row = rows.find(item => item.id === Number(path.split('/').pop()))!
        if (options.waitForPut && writes.length === 1) await options.waitForPut
        Object.assign(row, body)
        return json(route, row)
      }
      const row = { id: 99, ...body } as Location
      rows.push(row)
      return json(route, row)
    }
    return json(route, path.endsWith('/unread-count') ? { unread_count: 0 } : { data: [], total: 0 })
  })
  await page.goto('/commerce/retail')
  await expect(page.getByRole('heading', { name: '电商工作台' })).toBeVisible()
  await page.getByRole('tab', { name: tabs.retail, exact: true }).click()
  await expect(locationRow(page, '瑞遇-二两兔')).toBeVisible()
  return {
    rows, writes,
    async switchTenant() {
      identity = { ...adminUser, tenant_id: 22, tenant_name: '另一商户', system_code: 'LOCATION002' }
      rows.splice(0, rows.length, { id: 81, business_type: 'retail', name: '另一商户仓库', location_type: 'warehouse', status: 'active' })
      await page.evaluate(user => {
        localStorage.setItem('user', JSON.stringify(user))
        window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: user }))
      }, identity)
      await page.getByRole('button', { name: '刷新', exact: true }).first().click()
      await expect(locationRow(page, '另一商户仓库')).toBeVisible()
    },
  }
}

function locationRow(page: Page, name: string) {
  return page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: name })
}

async function switchDomain(page: Page, domain: Domain) {
  await page.evaluate(businessType => {
    window.history.pushState({}, '', `/commerce/${businessType}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, domain)
  await expect(page.getByRole('heading', { name: `${domain === 'retail' ? '电商' : '餐饮'}工作台` })).toBeVisible()
  await page.getByRole('tab', { name: tabs[domain], exact: true }).click()
}

for (const domain of ['retail', 'restaurant'] as const) test(`${domain} 编辑预填原资料并 PUT 原地点，库存选择器更新名称`, async ({ page }, testInfo) => {
  const { rows, writes } = await setup(page)
  if (domain === 'restaurant') await switchDomain(page, domain)
  const original = { ...rows.find(row => row.business_type === domain)! }
  await locationRow(page, original.name).getByRole('button', { name: '编辑', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: `编辑${nouns[domain]}`, exact: true })
  await expect(dialog.getByRole('textbox')).toHaveValue(original.name)
  await expect(dialog.locator('.el-select').nth(0)).toContainText(domain === 'retail' ? '发货点' : '取餐点')
  await expect(dialog.locator('.el-select').nth(1)).toContainText(domain === 'retail' ? '启用' : '停用')
  await dialog.getByRole('textbox').fill('  精选履约点  ')
  await dialog.locator('.el-select').nth(0).click()
  await page.getByRole('option', { name: domain === 'retail' ? '仓库' : '门店', exact: true }).click()
  if (domain === 'restaurant') {
    await dialog.locator('.el-select').nth(1).click()
    await page.getByRole('option', { name: '启用', exact: true }).click()
  }
  await expect(page.locator('.el-select__popper:visible')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath(`${domain}-location-edit.png`) })
  await dialog.getByRole('button', { name: `保存${nouns[domain]}`, exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(writes).toEqual([{ method: 'PUT', path: `/commerce/locations/${original.id}`, body: {
    business_type: domain, name: '精选履约点', location_type: domain === 'retail' ? 'warehouse' : 'store', status: 'active',
  } }])
  expect(rows.filter(row => row.business_type === domain)).toHaveLength(1)
  await expect(locationRow(page, '精选履约点')).toBeVisible()
  await expect(locationRow(page, original.name)).toHaveCount(0)
  await page.getByRole('tab', { name: '库存', exact: true }).click()
  await page.getByRole('button', { name: '设置库存', exact: true }).click()
  const inventoryDialog = page.getByRole('dialog', { name: '设置库存', exact: true })
  await expect(inventoryDialog.locator('.el-select').nth(1)).toContainText('精选履约点')
})

test('地点保存失败保留输入并允许重试同一地点', async ({ page }) => {
  const { writes } = await setup(page, { failFirst: true })
  await locationRow(page, '瑞遇-二两兔').getByRole('button', { name: '编辑', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑仓库/发货点', exact: true })
  await dialog.getByRole('textbox').fill('待重试的新名称')
  const save = dialog.getByRole('button', { name: '保存仓库/发货点', exact: true })
  await save.click()
  await expect(page.getByText('地点保存暂不可用', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('textbox')).toHaveValue('待重试的新名称')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(dialog).toHaveCount(0)
  expect(writes).toHaveLength(2)
  expect(writes[1]).toEqual(writes[0])
  expect(writes[1]!.path).toBe('/commerce/locations/41')
})

test('关闭编辑再新增会清空编辑 ID 和旧资料', async ({ page }) => {
  const { writes } = await setup(page)
  await locationRow(page, '瑞遇-二两兔').getByRole('button', { name: '编辑', exact: true }).click()
  await page.getByRole('dialog', { name: '编辑仓库/发货点', exact: true }).getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '新增仓库/发货点', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新增仓库/发货点', exact: true })
  await expect(dialog.getByRole('textbox')).toHaveValue('')
  await expect(dialog.locator('.el-select').nth(0)).toContainText('仓库')
  await dialog.getByRole('textbox').fill('  新发货仓库  ')
  await dialog.getByRole('button', { name: '保存仓库/发货点', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(writes).toEqual([{ method: 'POST', path: '/commerce/locations', body: { business_type: 'retail', name: '新发货仓库', location_type: 'warehouse', status: 'active' } }])
})

test('只读账号没有地点新增、编辑和状态变更入口', async ({ page }) => {
  const { writes } = await setup(page, { readOnly: true })
  await expect(page.getByRole('button', { name: '新增仓库/发货点', exact: true })).toHaveCount(0)
  const row = locationRow(page, '瑞遇-二两兔')
  await expect(row.getByText('只读', { exact: true })).toBeVisible()
  await expect(row.getByRole('button')).toHaveCount(0)
  expect(writes).toHaveLength(0)
})

for (const change of ['业务', '租户'] as const) test(`地点保存防重复，切换${change}后旧响应不能关闭新表单`, async ({ page }) => {
  const gate = deferred()
  const { writes, switchTenant } = await setup(page, { waitForPut: gate.promise })
  await locationRow(page, '瑞遇-二两兔').getByRole('button', { name: '编辑', exact: true }).click()
  const originalDialog = page.getByRole('dialog', { name: '编辑仓库/发货点', exact: true })
  await originalDialog.getByRole('textbox').fill('原仓库已修改')
  const save = originalDialog.getByRole('button', { name: '保存仓库/发货点', exact: true })
  await save.click()
  await expect.poll(() => writes.length).toBe(1)
  await expect(save).toBeDisabled()
  if (change === '业务') await switchDomain(page, 'restaurant')
  else await switchTenant()
  const domain: Domain = change === '业务' ? 'restaurant' : 'retail'
  await locationRow(page, change === '业务' ? '一号门店' : '另一商户仓库').getByRole('button', { name: '编辑', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: `编辑${nouns[domain]}`, exact: true })
  await dialog.getByRole('textbox').fill('当前地点待保存')
  const response = page.waitForResponse(res => res.request().method() === 'PUT' && new URL(res.url()).pathname.endsWith('/commerce/locations/41'))
  gate.release()
  await response
  await expect(dialog.getByRole('textbox')).toHaveValue('当前地点待保存')
  await expect(dialog.getByRole('button', { name: `保存${nouns[domain]}`, exact: true })).toBeEnabled()
  await dialog.getByRole('button', { name: `保存${nouns[domain]}`, exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(writes).toHaveLength(2)
  expect(writes[0]).toMatchObject({ method: 'PUT', path: '/commerce/locations/41', body: { business_type: 'retail' } })
  expect(writes[1]).toMatchObject({ method: 'PUT', path: `/commerce/locations/${change === '业务' ? 71 : 81}`, body: { business_type: domain, name: '当前地点待保存' } })
})
