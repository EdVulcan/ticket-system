import { expect, test, type Page, type Route } from '@playwright/test'

const adminUser = {
  id: 21,
  username: 'commerce-admin',
  role: 'admin',
  scope: 'tenant',
  tenant_id: 21,
  tenant_name: '营销测试商户',
  system_code: 'MARKETING001',
  capabilities: [],
  business_capabilities: [
    { business_type: 'restaurant', status: 'active' },
    { business_type: 'retail', status: 'active' },
  ],
}

const channel = { id: 11, code: 'wechat-demo', app_id: 'wx-demo', status: 'active' }

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function prepare(page: Page, coupons: unknown[] = [], user = adminUser) {
  await page.addInitScript(currentUser => {
    localStorage.setItem('token', 'commerce-marketing-token')
    localStorage.setItem('user', JSON.stringify(currentUser))
  }, user)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: user.tenant_id,
    name: user.tenant_name,
    system_code: user.system_code,
    status: 'active',
    capabilities: user.capabilities,
    business_capabilities: user.business_capabilities,
  }))
  await page.route('**/api/v1/commerce/storefront-channels', route => json(route, { data: [channel] }))
  await page.route('**/api/v1/commerce/notifications**', route => json(route, { data: [], unread_count: 0 }))
  await page.route('**/api/v1/commerce/promotions/coupon-templates**', route => json(route, { data: coupons }))
  await page.route('**/api/v1/commerce/promotions/assist-campaigns**', route => json(route, { data: [] }))
}

test('空营销列表提供对应创建按钮并引导先创建优惠券', async ({ page }) => {
  await prepare(page)
  await page.goto('/commerce/marketing')

  await expect(page.getByRole('heading', { name: '营销中心' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增优惠券模板' })).toHaveCount(2)
  await page.getByRole('button', { name: '新增优惠券模板' }).last().click()
  await expect(page.getByRole('dialog', { name: '新增优惠券模板' })).toBeVisible()
  await page.getByRole('dialog', { name: '新增优惠券模板' }).getByRole('button', { name: '取消' }).click()

  await page.getByRole('tab', { name: '分享助力' }).click()
  await expect(page.getByRole('button', { name: '先创建优惠券模板' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增分享助力活动' })).toHaveCount(0)
  await page.getByRole('button', { name: '先创建优惠券模板' }).click()
  await expect(page.getByRole('dialog', { name: '新增优惠券模板' })).toBeVisible()
})

test('已有优惠券时分享助力空列表提供创建活动按钮', async ({ page }) => {
  await prepare(page, [{ id: 31, name: '新客优惠', business_types: ['restaurant', 'retail'], discount_cents: 100, min_goods_subtotal_cents: 0, valid_days: 7, status: 'draft' }])
  await page.goto('/commerce/marketing')
  await page.getByRole('tab', { name: '分享助力' }).click()

  await expect(page.getByRole('button', { name: '新增分享助力活动' })).toHaveCount(2)
  await page.getByRole('button', { name: '新增分享助力活动' }).last().click()
  await expect(page.getByRole('dialog', { name: '新增分享助力活动' })).toBeVisible()
})

test('只读账号不显示营销写入按钮', async ({ page }) => {
  const viewer = { ...adminUser, role: 'viewer', permissions: ['catalog.read'] }
  await prepare(page, [], viewer)
  await page.goto('/commerce/marketing')

  await expect(page.getByRole('heading', { name: '营销中心' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新增优惠券模板' })).toHaveCount(0)
  await page.getByRole('tab', { name: '分享助力' }).click()
  await expect(page.getByRole('button', { name: '先创建优惠券模板' })).toHaveCount(0)
})
