import { expect, test, type Page, type Route } from '@playwright/test'

const commerceUser = {
  id: 21,
  username: 'commerce-refresh-admin',
  role: 'admin',
  scope: 'tenant',
  tenant_id: 21,
  tenant_name: '刷新竞态测试商户',
  system_code: 'REFRESH001',
  capabilities: [],
  permissions: ['catalog.read', 'catalog.write', 'orders.read', 'operations.write'],
  business_capabilities: [
    { business_type: 'restaurant', status: 'active' },
    { business_type: 'retail', status: 'active' },
  ],
}

const platformUser = {
  id: 1,
  username: 'platform-refresh-admin',
  role: 'platform_admin',
  scope: 'platform',
  tenant_id: null,
  tenant_name: '',
  system_code: 'PLATFORM001',
  capabilities: [],
  permissions: [],
}

const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

function product(id: number, name: string) {
  return {
    id,
    name,
    short_title: '',
    category_name: '测试分类',
    description: '',
    status: 'online',
    skus: [{ id: id * 10, price_cents: 1000, original_price_cents: 1000 }],
    media: [],
  }
}

async function prepare(
  page: Page,
  user: typeof commerceUser | typeof platformUser,
  handler: (route: Route, path: string, url: URL) => Promise<boolean>,
) {
  await page.addInitScript(currentUser => {
    localStorage.setItem('token', 'refresh-race-token')
    localStorage.setItem('user', JSON.stringify(currentUser))
  }, user)
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^.*\/api\/v1/, '')
    if (path === '/tenants/me') {
      await json(route, user)
      return
    }
    if (path === '/commerce/notifications' || path === '/commerce/notifications/unread-count') {
      await json(route, path.endsWith('unread-count') ? { unread_count: 0 } : { data: [] })
      return
    }
    if (await handler(route, path, url)) return
    await json(route, { data: [], total: 0 })
  })
}

test('商品详情关闭后再打开其他商品不会被旧响应覆盖', async ({ page }) => {
  let firstProductRequestStarted = false
  const first = product(101, 'A菜品')
  const second = product(202, 'B菜品')

  await prepare(page, commerceUser, async (route, path) => {
    if (path === '/commerce/products') {
      await json(route, { data: [first, second], total: 2 })
      return true
    }
    if (path === '/commerce/locations' || path === '/commerce/inventory') {
      await json(route, { data: [] })
      return true
    }
    const optionsMatch = path.match(/^\/commerce\/products\/(\d+)\/options$/)
    const productMatch = path.match(/^\/commerce\/products\/(\d+)$/)
    const id = Number(optionsMatch?.[1] || productMatch?.[1] || 0)
    if (id) {
      if (id === first.id) {
        firstProductRequestStarted = true
        await wait(1200)
      }
      if (optionsMatch) {
        await json(route, { data: [{ id: id * 100, name: id === second.id ? 'B规格' : 'A规格', options: [] }] })
      } else {
        await json(route, id === first.id ? first : second)
      }
      return true
    }
    return false
  })

  await page.goto('/commerce/restaurant')
  await expect(page.getByRole('heading', { name: '餐饮工作台' })).toBeVisible()

  const firstRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: first.name })
  const secondRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: second.name })
  await firstRow.getByRole('button', { name: '管理规格/选项' }).click()
  await expect.poll(() => firstProductRequestStarted).toBe(true)
  const dialog = page.getByRole('dialog', { name: '菜品详情' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()

  await secondRow.getByRole('button', { name: '管理规格/选项' }).click()
  await expect(dialog.getByRole('heading', { name: second.name })).toBeVisible()
  await expect(dialog.getByText('B规格', { exact: true })).toBeVisible()
  await wait(1400)
  await expect(dialog.getByRole('heading', { name: second.name })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: first.name })).toHaveCount(0)
})

test('切换商业业务会关闭商品编辑窗', async ({ page }) => {
  const row = product(301, '餐饮商品')
  await prepare(page, commerceUser, async (route, path) => {
    if (path === '/commerce/products') {
      await json(route, { data: [row], total: 1 })
      return true
    }
    if (path === '/commerce/locations' || path === '/commerce/inventory') {
      await json(route, { data: [] })
      return true
    }
    const optionsMatch = path.match(/^\/commerce\/products\/(\d+)\/options$/)
    const productMatch = path.match(/^\/commerce\/products\/(\d+)$/)
    if (optionsMatch) {
      await json(route, { data: [] })
      return true
    }
    if (productMatch) {
      await json(route, row)
      return true
    }
    return false
  })

  await page.goto('/commerce/retail')
  await page.getByRole('menuitem', { name: '餐饮工作台' }).click()
  await expect(page.getByRole('heading', { name: '餐饮工作台' })).toBeVisible()
  const rowLocator = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: row.name })
  await rowLocator.getByRole('button', { name: '管理规格/选项' }).click()
  const detailDialog = page.getByRole('dialog', { name: '菜品详情' })
  await expect(detailDialog).toBeVisible()
  await detailDialog.getByRole('button', { name: '编辑基本信息' }).click()
  await expect(page.getByRole('dialog', { name: '编辑菜品基本信息' })).toBeVisible()

  await page.evaluate(() => {
    window.history.pushState({}, '', '/commerce/retail')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
  await page.waitForURL('**/commerce/retail')
  await expect(page.getByRole('heading', { name: '电商工作台' })).toBeVisible()
  await expect(page.getByRole('dialog', { name: /编辑.*基本信息/ })).toHaveCount(0)
})

test('切换商业业务后旧商品编辑响应不能写回新详情', async ({ page }) => {
  let editRequestStarted = false
  const retailProduct = product(351, '电商商品')
  const restaurantProduct = product(352, '餐饮商品')

  await prepare(page, commerceUser, async (route, path, url) => {
    if (path === '/commerce/products') {
      const businessType = url.searchParams.get('business_type')
      await json(route, { data: [businessType === 'restaurant' ? restaurantProduct : retailProduct], total: 1 })
      return true
    }
    if (path === '/commerce/locations' || path === '/commerce/inventory') {
      await json(route, { data: [] })
      return true
    }
    if (path === `/commerce/products/${retailProduct.id}` && route.request().method() === 'PUT') {
      editRequestStarted = true
      await wait(1200)
      await json(route, { ...retailProduct, name: '旧业务已保存' })
      return true
    }
    const optionsMatch = path.match(/^\/commerce\/products\/(\d+)\/options$/)
    const productMatch = path.match(/^\/commerce\/products\/(\d+)$/)
    if (optionsMatch) {
      await json(route, { data: [] })
      return true
    }
    if (productMatch) {
      await json(route, Number(productMatch[1]) === restaurantProduct.id ? restaurantProduct : retailProduct)
      return true
    }
    return false
  })

  await page.goto('/commerce/retail')
  const retailRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: retailProduct.name })
  await retailRow.getByRole('button', { name: '管理 SKU / 规格' }).click()
  const retailDetail = page.getByRole('dialog', { name: '商品详情' })
  await expect(retailDetail).toBeVisible()
  await retailDetail.getByRole('button', { name: '编辑基本信息' }).click()
  const editDialog = page.getByRole('dialog', { name: '编辑商品基本信息' })
  await editDialog.getByRole('textbox').first().fill('旧业务已保存')
  await editDialog.getByRole('button', { name: '保存修改' }).click()
  await expect.poll(() => editRequestStarted).toBe(true)

  await page.evaluate(() => {
    window.history.pushState({}, '', '/commerce/restaurant')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
  await page.waitForURL('**/commerce/restaurant')
  await expect(page.getByRole('heading', { name: '餐饮工作台' })).toBeVisible()
  const restaurantRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: restaurantProduct.name })
  await restaurantRow.getByRole('button', { name: '管理规格/选项' }).click()
  const restaurantDetail = page.getByRole('dialog', { name: '菜品详情' })
  await expect(restaurantDetail.getByRole('heading', { name: restaurantProduct.name })).toBeVisible()
  await wait(1400)
  await expect(restaurantDetail.getByRole('heading', { name: restaurantProduct.name })).toBeVisible()
  await expect(restaurantDetail.getByRole('heading', { name: retailProduct.name })).toHaveCount(0)
})

test('订单详情关闭后再打开其他订单不会被旧订单或 shipment 响应覆盖', async ({ page }) => {
  let firstOrderRequestStarted = false
  const first = { id: 401, order_no: 'ORD-A', business_type: 'retail', payment_status: 'paid', fulfillment_status: 'pending_shipment', refund_status: 'none', items: [] }
  const second = { id: 402, order_no: 'ORD-B', business_type: 'retail', payment_status: 'paid', fulfillment_status: 'pending_shipment', refund_status: 'none', items: [] }

  await prepare(page, commerceUser, async (route, path) => {
    if (path === '/commerce/products' || path === '/commerce/locations' || path === '/commerce/inventory') {
      await json(route, path === '/commerce/products' ? { data: [], total: 0 } : { data: [] })
      return true
    }
    if (path === '/commerce/orders') {
      await json(route, { data: [first, second], total: 2 })
      return true
    }
    const shipmentMatch = path.match(/^\/commerce\/orders\/(\d+)\/shipment$/)
    const orderMatch = path.match(/^\/commerce\/orders\/(\d+)$/)
    const id = Number(shipmentMatch?.[1] || orderMatch?.[1] || 0)
    if (id) {
      if (id === first.id) {
        firstOrderRequestStarted = true
        await wait(1200)
      }
      if (shipmentMatch) {
        await json(route, { shipment: { id }, timeline: [] })
      } else {
        await json(route, id === first.id ? first : second)
      }
      return true
    }
    return false
  })

  await page.goto('/commerce/retail?tab=orders')
  await expect(page.getByText(first.order_no, { exact: true })).toBeVisible()
  const firstRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: first.order_no })
  const secondRow = page.locator('.commerce-table .el-table__body-wrapper tbody tr').filter({ hasText: second.order_no })
  await firstRow.getByRole('button', { name: '详情' }).click()
  await expect.poll(() => firstOrderRequestStarted).toBe(true)
  const dialog = page.getByRole('dialog', { name: '订单详情' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()

  await secondRow.getByRole('button', { name: '详情' }).click()
  await expect(dialog.getByText(second.order_no, { exact: true })).toBeVisible()
  await wait(1400)
  await expect(dialog.getByText(second.order_no, { exact: true })).toBeVisible()
  await expect(dialog.getByText(first.order_no, { exact: true })).toHaveCount(0)
})

test('切换商业业务不会让旧业务订单响应回填新工作台', async ({ page }) => {
  let oldOrderRequestStarted = false
  const oldOrder = { id: 501, order_no: '旧餐饮订单', business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'none', items: [] }
  const newOrder = { id: 502, order_no: '新电商订单', business_type: 'retail', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'none', items: [] }

  await prepare(page, commerceUser, async (route, path, url) => {
    if (path === '/commerce/products' || path === '/commerce/locations' || path === '/commerce/inventory') {
      await json(route, path === '/commerce/products' ? { data: [], total: 0 } : { data: [] })
      return true
    }
    if (path === '/commerce/orders') {
      const businessType = url.searchParams.get('business_type')
      if (businessType === 'restaurant') {
        oldOrderRequestStarted = true
        await wait(1000)
        await json(route, { data: [oldOrder], total: 1 })
      } else {
        await wait(2500)
        await json(route, { data: [newOrder], total: 1 })
      }
      return true
    }
    return false
  })

  await page.goto('/commerce/restaurant?tab=orders')
  await expect.poll(() => oldOrderRequestStarted).toBe(true)
  await page.getByRole('menuitem', { name: '电商工作台' }).click()
  await expect(page.getByRole('heading', { name: '电商工作台' })).toBeVisible()
  await wait(1300)
  await page.getByRole('tab', { name: '订单与发货' }).click()

  await expect(page.getByText(oldOrder.order_no, { exact: true })).toHaveCount(0, { timeout: 500 })
  await expect(page.getByText(newOrder.order_no, { exact: true })).toBeVisible({ timeout: 4000 })
})

test('平台运营租户筛选不会被旧租户响应覆盖', async ({ page }) => {
  let tenantOneRequestStarted = false
  await prepare(page, platformUser, async (route, path, url) => {
    if (!path.startsWith('/platform/')) return false
    const tenantID = Number(url.searchParams.get('tenant_id') || 0)
    if (tenantID === 1) {
      tenantOneRequestStarted = true
      await wait(350)
    }
    const row = {
      tenant_id: tenantID,
      tenant_name: `租户${tenantID || '全部'}`,
      order_no: `订单-${tenantID || '全部'}`,
      description: `异常-${tenantID || '全部'}`,
      name: `设备-${tenantID || '全部'}`,
      statement_no: `结算-${tenantID || '全部'}`,
      action: 'platform.orders.read',
      status: 'paid',
      total_amount: '10.00',
      created_at: '2026-10-14T10:00:00+08:00',
    }
    if (path === '/platform/finance') {
      await json(route, { pending_payment_cents: tenantID * 100 })
    } else if (path === '/platform/orders') {
      await json(route, { data: [row], total: 1 })
    } else {
      await json(route, { data: [row], total: 1 })
    }
    return true
  })

  await page.goto('/platform-operations')
  await expect(page.getByRole('heading', { name: '平台运营工作台' })).toBeVisible()
  const tenantInput = page.getByPlaceholder('目标租户编号（可选）')
  await tenantInput.fill('1')
  await page.getByRole('button', { name: '查询' }).click()
  await expect.poll(() => tenantOneRequestStarted).toBe(true)
  await tenantInput.fill('2')
  await page.getByRole('button', { name: '查询' }).click()
  await expect(page.getByText('订单-2', { exact: true })).toBeVisible()
  await wait(450)
  await expect(page.getByText('订单-2', { exact: true })).toBeVisible()
  await expect(page.getByText('订单-1', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '异常工作台' }).click()
  await expect(page.getByText('异常-2', { exact: true })).toBeVisible()
  await expect(page.getByText('异常-1', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '资金总览' }).click()
  await expect(page.getByText('2.00', { exact: true })).toBeVisible()
  await expect(page.getByText('1.00', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '设备总览' }).click()
  await expect(page.getByText('设备-2', { exact: true })).toBeVisible()
  await expect(page.getByText('设备-1', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '结算总览' }).click()
  await expect(page.getByText('结算-2', { exact: true })).toBeVisible()
  await expect(page.getByText('结算-1', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '审计日志' }).click()
  await expect(page.getByLabel('审计日志').getByText('租户2', { exact: true })).toBeVisible()
  await expect(page.getByLabel('审计日志').getByText('租户1', { exact: true })).toHaveCount(0)
})
