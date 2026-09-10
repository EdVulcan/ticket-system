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

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepareChannel(page: Page, user = supplierUser) {
  await page.addInitScript(currentUser => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(currentUser))
  }, user)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: user.tenant_id,
    name: user.tenant_name,
    system_code: user.system_code,
    status: 'active',
    capabilities: user.capabilities,
    supplier_business_types: user.supplier_business_types,
  }))
  await page.route('**/api/v1/channel-accounts', route => json(route, { data: [account] }))
}

test('随机立减仅在启用后要求完整配置，并以整数分保存', async ({ page }) => {
  await prepareChannel(page)
  const savedPayloads: unknown[] = []
  await page.route('**/api/v1/channel-accounts/7/instant-discount', async route => {
    if (route.request().method() === 'PUT') {
      savedPayloads.push(route.request().postDataJSON())
      return json(route, {})
    }
    return json(route, {
      enabled: false,
      min_discount_cents: 0,
      max_discount_cents: 0,
      validity_minutes: 0,
      cooldown_days: 0,
      mapping_ids: [32],
      products: [
        { id: 31, name: '成人票', price_cents: 9900, eligible: true },
        { id: 32, name: '已下架票', price_cents: 5900, eligible: false },
      ],
    })
  })

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '随机立减', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '随机立减' })
  const save = dialog.getByRole('button', { name: '保存' })
  await expect(save).toBeEnabled()
  await expect(dialog.getByRole('checkbox', { name: '已下架票' })).toBeChecked()
  await expect(dialog.getByText('当前不可参与', { exact: true })).toBeVisible()

  await dialog.locator('.el-switch').click()
  await expect(save).toBeDisabled()
  await dialog.locator('.el-checkbox').filter({ hasText: '成人票' }).click()
  const minimum = dialog.locator('.el-form-item').filter({ hasText: '随机立减最低金额' }).getByRole('spinbutton')
  const maximum = dialog.locator('.el-form-item').filter({ hasText: '随机立减最高金额' }).getByRole('spinbutton')
  const validity = dialog.locator('.el-form-item').filter({ hasText: '立减有效期' }).getByRole('spinbutton')
  const cooldown = dialog.locator('.el-form-item').filter({ hasText: '再次获得间隔' }).getByRole('spinbutton')
  await minimum.fill('1.25')
  await maximum.fill('3.50')
  await validity.fill('30')
  await cooldown.fill('7')
  await expect(save).toBeDisabled()
  await dialog.locator('.el-checkbox').filter({ hasText: '已下架票' }).click()
  await expect(save).toBeEnabled()
  await save.click()
  await expect.poll(() => savedPayloads).toEqual([{
    enabled: true,
    min_discount_cents: 125,
    max_discount_cents: 350,
    validity_minutes: 30,
    cooldown_days: 7,
    mapping_ids: [31],
  }])
})

test('只读用户可以查看随机立减配置但不能保存', async ({ page }) => {
  const viewer = { ...supplierUser, role: 'viewer', permissions: ['channels.read'] }
  await prepareChannel(page, viewer)
  await page.route('**/api/v1/channel-accounts/7/instant-discount', route => json(route, {
    enabled: true,
    min_discount_cents: 100,
    max_discount_cents: 300,
    validity_minutes: 60,
    cooldown_days: 3,
    mapping_ids: [31],
    products: [{ id: 31, name: '成人票', price_cents: 9900, eligible: true }],
  }))

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '随机立减', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '随机立减' })
  await expect(dialog.getByRole('checkbox', { name: '成人票' })).toBeChecked()
  await expect(dialog.getByRole('button', { name: '保存' })).toHaveCount(0)
  await expect(dialog.getByRole('switch')).toBeDisabled()
})
