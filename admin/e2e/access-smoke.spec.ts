import { expect, test, type Page } from '@playwright/test'

const tenantUser = {
  id: 1,
  username: 'admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '示例景区',
  system_code: 'SCENIC001',
  capabilities: [{ capability: 'supplier', status: 'active' }],
}

async function mockJSON(page: Page, pattern: string, body: unknown) {
  await page.route(pattern, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  }))
}

test('未登录访问时不会短暂显示后台界面', async ({ page }) => {
  await page.addInitScript(() => {
    const state = window as Window & { __authenticatedShellSeen?: boolean }
    state.__authenticatedShellSeen = false
    const observer = new MutationObserver(() => {
      const sidebar = document.querySelector('aside')
      if (sidebar?.textContent?.includes('景区票务平台')) {
        state.__authenticatedShellSeen = true
      }
    })
    observer.observe(document, { childList: true, subtree: true })
  })

  await page.goto('/login')
  await expect(page.getByRole('heading', { name: '景区票务管理系统' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => (
    window as Window & { __authenticatedShellSeen?: boolean }
  ).__authenticatedShellSeen)).toBe(false)
})

test('景区租户登录后只显示已授权工作区', async ({ page }) => {
  await mockJSON(page, '**/api/v1/auth/login', { token: 'tenant-token', user: tenantUser })
  await page.goto('/login')

  await expect(page.getByText('平台登录', { exact: true })).toHaveCount(0)
  await page.getByPlaceholder('商户系统编号').fill('SCENIC001')
  await page.getByPlaceholder('用户名').fill('admin')
  await page.getByPlaceholder('密码').fill('tenant-password')
  await page.getByRole('button', { name: '登 录' }).click()

  await expect(page.getByRole('heading', { name: '经营控制台' })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await expect(page.getByText('商户开户管理', { exact: true })).toHaveCount(0)
})

test('平台身份不能进入租户业务菜单', async ({ page }) => {
  await mockJSON(page, '**/api/v1/auth/platform/login', {
    token: 'platform-token',
    user: { id: 9, username: 'platform_admin', role: 'platform_admin', scope: 'platform' },
  })
  await mockJSON(page, '**/api/v1/platform/overview', {})
  await page.goto('/platform/login')

  await expect(page.getByPlaceholder('商户系统编号')).toHaveCount(0)
  await page.getByPlaceholder('平台用户名').fill('platform_admin')
  await page.getByPlaceholder('密码').fill('platform-password')
  await page.getByRole('button', { name: '登 录' }).click()

  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
  await expect(page.getByText('商户开户管理', { exact: true })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '平台账号' })).toBeVisible()
  await expect(page.getByTestId('account-context')).toHaveText('系统服务商')
  await expect(page.getByRole('menuitem', { name: '平台运营工作台' })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toHaveCount(0)

  await page.goto('/product')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
})

test('平台运营员只能进入只读运营工作台', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('token', 'platform-operator-token')
    localStorage.setItem('user', JSON.stringify({ id: 10, username: 'operator', role: 'platform_operator', scope: 'platform' }))
  })
  await mockJSON(page, '**/api/v1/platform/overview', {})
  await page.goto('/')

  await expect(page.getByRole('heading', { name: '平台运行总览' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '平台运营工作台' })).toBeVisible()
  await expect(page.getByText('商户开户管理', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '平台账号' })).toHaveCount(0)

  await page.goto('/tenant')
  await expect(page).toHaveURL('http://127.0.0.1:4173/')
})

test('平台管理员可以查看平台账号并自行修改密码', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('token', 'platform-token')
    localStorage.setItem('user', JSON.stringify({ id: 9, username: 'platform_admin', role: 'platform_admin', scope: 'platform', is_initial_admin: true }))
  })
  await mockJSON(page, '**/api/v1/platform-users', [
    { id: 9, username: 'platform_admin', role: 'platform_admin', status: 'active', is_initial_admin: true },
    { id: 10, username: 'operator', role: 'platform_operator', status: 'active', is_initial_admin: false },
  ])
  await mockJSON(page, '**/api/v1/auth/password', { message: 'password changed' })

  await page.goto('/platform-users')
  await expect(page.getByRole('heading', { name: '平台账号' })).toBeVisible()
  await expect(page.getByText('平台运营员', { exact: true })).toBeVisible()

  await page.getByTestId('profile-menu').click()
  await page.getByRole('menuitem', { name: '修改密码' }).click()
  await page.getByLabel('当前密码').fill('old-password')
  await page.getByLabel('新密码', { exact: true }).fill('new-password')
  await page.getByLabel('确认新密码').fill('new-password')
  await page.getByRole('button', { name: '确认修改' }).click()
  await expect(page).toHaveURL('http://127.0.0.1:4173/platform/login')
})

test('商户业务能力排列清晰且强制下线有明确确认', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('token', 'platform-token')
    localStorage.setItem('user', JSON.stringify({ id: 9, username: 'platform_admin', role: 'platform_admin', scope: 'platform' }))
  })
  await mockJSON(page, '**/api/v1/tenants?*', {
    data: [{
      id: 1,
      name: '示例景区',
      system_code: 'SCENIC001',
      status: 'active',
      qualification_status: 'approved',
      capabilities: [
        { capability: 'supplier', status: 'active' },
        { capability: 'distributor', status: 'disabled' },
        { capability: 'travel_agency', status: 'disabled' },
      ],
    }],
    total: 1,
  })

  await page.goto('/tenant')
  const capabilityButtons = [
    page.getByRole('button', { name: '景区供应商 · 已启用' }),
    page.getByRole('button', { name: '分销商 · 未启用' }),
    page.getByRole('button', { name: '旅行社 · 未启用' }),
  ]
  const boxes = await Promise.all(capabilityButtons.map(async button => {
    await expect(button).toBeVisible()
    return button.boundingBox()
  }))
  const visibleBoxes = boxes.filter((box): box is NonNullable<typeof box> => box !== null)
  const orderedBoxes = [...visibleBoxes].sort((left, right) => left.y - right.y)
  expect(orderedBoxes[1].y - (orderedBoxes[0].y + orderedBoxes[0].height)).toBeGreaterThanOrEqual(6)
  expect(orderedBoxes[2].y - (orderedBoxes[1].y + orderedBoxes[1].height)).toBeGreaterThanOrEqual(6)

  await page.getByRole('button', { name: '强制下线' }).click()
  await expect(page.getByText('用户需要重新登录。此操作不会删除账号或业务数据。', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '取消' }).click()
})

test('旅行社能力可以进入团队工作区', async ({ page }) => {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'travel-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, {
    ...tenantUser,
    tenant_name: '示例旅行社',
    capabilities: [{ capability: 'travel_agency', status: 'active' }],
  })
  await mockJSON(page, '**/api/v1/teams?*', { data: [], total: 0 })

  await page.goto('/teams')
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
  await expect(page.getByRole('button', { name: '新建团队' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '旅行社团队' })).toBeVisible()
  await expect(page.getByText('线上门票', { exact: true })).toHaveCount(0)
})
