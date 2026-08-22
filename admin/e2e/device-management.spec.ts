import { expect, test, type Page, type Route } from '@playwright/test'

const tenantUser = {
  id: 1,
  username: 'admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '示例景区',
  system_code: 'SCENIC001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}

const json = (route: Route, body: unknown) => route.fulfill({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
})

async function loginAsSupplier(page: Page) {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'device-test-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, tenantUser)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: tenantUser.tenant_id,
    name: tenantUser.tenant_name,
    system_code: tenantUser.system_code,
    status: 'active',
    capabilities: tenantUser.capabilities,
    supplier_business_types: tenantUser.supplier_business_types,
  }))
}

test('设备管理按类型切换并支持刷新当前列表', async ({ page }) => {
  await loginAsSupplier(page)
  const requests: string[] = []
  await page.route('**/api/v1/devices*', route => {
    const url = new URL(route.request().url())
    const type = url.searchParams.get('type') || 'all'
    requests.push(type)
    const rows = type === 'gate'
      ? [{ id: 1, name: '正门闸机', serial_number: 'GATE-1', type: 'gate', status: 'offline' }]
      : type === 'handheld'
        ? [{ id: 2, name: '巡检手持机', serial_number: 'HAND-1', type: 'handheld', status: 'offline' }]
        : type === 'pos'
          ? [{ id: 3, name: '窗口终端', serial_number: 'POS-1', type: 'pos', status: 'offline' }]
          : [
            { id: 1, name: '正门闸机', serial_number: 'GATE-1', type: 'gate', status: 'offline' },
            { id: 2, name: '巡检手持机', serial_number: 'HAND-1', type: 'handheld', status: 'offline' },
          ]
    return json(route, { data: rows, total: rows.length, page: 1 })
  })
  await page.route('**/api/v1/checkpoints?*', route => json(route, { data: [], total: 0 }))

  await page.goto('/device')
  await expect(page.getByRole('heading', { name: '设备管理' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '全部设备' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '闸机' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '手持机' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '桌面终端' })).toBeVisible()
  await expect(page.getByText('正门闸机')).toBeVisible()
  await expect(page.getByText('巡检手持机')).toBeVisible()

  await page.getByRole('tab', { name: '闸机' }).click()
  await expect(page.getByText('正门闸机')).toBeVisible()
  await expect(page.getByText('巡检手持机')).toHaveCount(0)
  await expect.poll(() => requests[requests.length - 1]).toBe('gate')

  const beforeRefresh = requests.length
  await page.getByRole('button', { name: '刷新设备列表' }).click()
  await expect.poll(() => requests.length).toBe(beforeRefresh + 1)
  await expect.poll(() => requests[requests.length - 1]).toBe('gate')
})
