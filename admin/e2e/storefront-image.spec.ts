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
const imageFile = { name: 'storefront.png', mimeType: 'image/png', buffer: Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]) }
const previewImage = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL8owAAAABJRU5ErkJggg==', 'base64')

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
  await page.route('https://example.test/**', route => route.fulfill({ contentType: 'image/png', body: previewImage }))
}

test('商城图片独立保存和清除，不读取或修改商品映射', async ({ page }) => {
  await prepareChannel(page)
  let storefrontImageURL = 'https://example.test/original-storefront.png'
  const savedPayloads: unknown[] = []
  let mappingCalls = 0
  await page.route('**/api/v1/channel-accounts/7/storefront', async route => {
    if (route.request().method() === 'GET') return json(route, { image_url: storefrontImageURL })
    savedPayloads.push(route.request().postDataJSON())
    storefrontImageURL = route.request().postDataJSON().image_url
    return json(route, { image_url: storefrontImageURL })
  })
  await page.route('**/api/v1/channel-accounts/7/storefront-image', route => json(route, { image_url: 'https://example.test/replaced-storefront.png' }))
  await page.route('**/api/v1/channel-accounts/mappings?*', async route => { mappingCalls += 1; await json(route, { data: [] }) })

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '商城图片', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商城图片' })
  await expect(dialog.locator('img')).toHaveAttribute('src', storefrontImageURL)
  await dialog.locator('input[type=file]').setInputFiles(imageFile)
  await expect(dialog.locator('img')).toHaveAttribute('src', 'https://example.test/replaced-storefront.png')
  await dialog.getByRole('button', { name: '保存' }).click()
  await expect.poll(() => savedPayloads).toEqual([{ image_url: 'https://example.test/replaced-storefront.png' }])
  if (process.env.STOREFRONT_SCREENSHOT) await page.screenshot({ path: process.env.STOREFRONT_SCREENSHOT })

  await dialog.getByRole('button', { name: '移除' }).click()
  await dialog.getByRole('button', { name: '保存' }).click()
  await expect.poll(() => savedPayloads).toEqual([
    { image_url: 'https://example.test/replaced-storefront.png' },
    { image_url: '' },
  ])
  expect(mappingCalls).toBe(0)
})

test('商城图片上传失败时保留已保存图片且不自动保存', async ({ page }) => {
  await prepareChannel(page)
  const original = 'https://example.test/original-storefront.png'
  let saveCalls = 0
  await page.route('**/api/v1/channel-accounts/7/storefront', async route => {
    if (route.request().method() === 'PUT') saveCalls += 1
    await json(route, { image_url: original })
  })
  await page.route('**/api/v1/channel-accounts/7/storefront-image', route => json(route, { error: '图片上传失败' }, 502))

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '商城图片', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商城图片' })
  await dialog.locator('input[type=file]').setInputFiles(imageFile)
  await expect(dialog.locator('img')).toHaveAttribute('src', original)
  expect(saveCalls).toBe(0)
})

test('商城图片加载失败时不能清空保存，重试后恢复已保存状态', async ({ page }) => {
  await prepareChannel(page)
  let reads = 0
  let saveCalls = 0
  await page.route('**/api/v1/channel-accounts/7/storefront', async route => {
    if (route.request().method() === 'PUT') { saveCalls += 1; return json(route, { image_url: '' }) }
    reads += 1
    if (reads === 1) return json(route, { error: '暂时不可用' }, 502)
    return json(route, { image_url: 'https://example.test/recovered-storefront.png' })
  })

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '商城图片', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商城图片' })
  await expect(dialog.getByText('商城图片暂时无法加载，请重试')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '保存' })).toBeDisabled()
  expect(saveCalls).toBe(0)

  await dialog.getByRole('button', { name: '重新加载' }).click()
  await expect(dialog.locator('img')).toHaveAttribute('src', 'https://example.test/recovered-storefront.png')
  await expect(dialog.getByRole('button', { name: '保存' })).toBeEnabled()
  expect(saveCalls).toBe(0)
})

test('只读用户可以查看商城图片但没有写入操作', async ({ page }) => {
  const viewer = { ...supplierUser, role: 'viewer', permissions: ['channels.read'] }
  await prepareChannel(page, viewer)
  await page.route('**/api/v1/channel-accounts/7/storefront', route => json(route, { image_url: 'https://example.test/readonly-storefront.png' }))

  await page.goto('/channels')
  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '商城图片', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '商城图片' })
  await expect(dialog.locator('img')).toHaveAttribute('src', 'https://example.test/readonly-storefront.png')
  await expect(dialog.getByRole('button', { name: '保存' })).toHaveCount(0)
  await expect(dialog.locator('input[type=file]')).toHaveCount(0)
})
