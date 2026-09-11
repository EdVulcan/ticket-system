import { expect, test, type Page, type Route } from '@playwright/test'

const json = (body: unknown, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })

const setupMobileRoutes = async (page: Page, verify: (route: Route) => Promise<void> | void) => {
  await page.route('**/api/v1/auth/staff/login', route => route.fulfill(json({ token: 'mobile-staff-token', staff: { id: 7, name: '验票员', job_number: '1001' } })))
  await page.route('**/api/v1/tenants/me', route => route.fulfill(json({ id: 1, name: '测试景区', system_code: 'SYS001' })))
  await page.route('**/api/v1/mobile/targets', route => route.fulfill(json({
    checkpoints: [{ id: 11, name: '南门', location: '游客中心', scenic_area_id: 3 }],
    devices: [{ id: 21, name: '南门手机终端', serial_number: 'WEB-001', type: 'handheld', status: 'offline', check_point_id: 11, scenic_area_id: 3 }],
  })))
  await page.route('**/api/v1/mobile/sessions', route => route.fulfill(json({
    session_token: 'mobile-session-token', expires_at: '2099-01-01T00:00:00Z',
    checkpoint: { id: 11, name: '南门' }, device: { id: 21, name: '南门手机终端', serial_number: 'WEB-001' },
  }, 201)))
  await page.route('**/api/v1/mobile/session/heartbeat', route => route.fulfill(json({ status: 'active' })))
  await page.route('**/api/v1/mobile/session/close', route => route.fulfill(json({ status: 'closed' })))
  await page.route('**/api/v1/mobile/session/verify', verify)
}

const enterSession = async (page: Page) => {
  await page.goto('/mobile')
  const mobilePage = page.locator('.mobile-verify-page')
  await mobilePage.getByLabel('系统编号').fill('SYS001')
  await mobilePage.getByLabel('员工工号').fill('1001')
  await mobilePage.getByLabel('密码').fill('password')
  await page.getByRole('button', { name: '登录并开始' }).click()
  await expect(page.getByRole('heading', { name: '选择核销点位' })).toBeVisible()
  await page.getByRole('button', { name: '进入核销' }).click()
  await expect(page.getByRole('button', { name: '打开相机扫码' })).toBeVisible()
}

test('手机核销登录、选点、备用输入和成功结果可用', async ({ page }) => {
  await setupMobileRoutes(page, route => route.fulfill(json({
    code: 200, result: 'allow', reason_code: 'verified', display_text: '欢迎光临\n成人票', voice_code: 'welcome', open_duration: 0,
  })))

  await enterSession(page)
  await page.getByRole('button', { name: '输入票码' }).click()
  await expect(page.getByRole('dialog', { name: '输入票码' })).toBeVisible()
  await page.getByRole('dialog').getByPlaceholder('输入票码').fill('TICKET-001')
  await page.getByRole('dialog').getByRole('button', { name: '核销' }).click()

  await expect(page.getByText('核销成功')).toBeVisible()
  await expect(page.getByText('欢迎光临', { exact: false })).toBeVisible()
  await expect(page.getByRole('heading', { name: '最近核销' })).toBeVisible()
  await expect(page.getByTestId('connection-status')).toHaveText('连接正常')
  await expect(page.getByRole('button', { name: '继续扫码' })).toBeVisible()
})

test('网络结果不明时锁定下一张票并复用同一请求号重试', async ({ page }) => {
  const requestIDs: string[] = []
  let attempt = 0
  await setupMobileRoutes(page, async route => {
    const body = route.request().postDataJSON() as { request_id: string }
    requestIDs.push(body.request_id)
    attempt += 1
    if (attempt === 1) {
      await route.abort()
      return
    }
    await route.fulfill(json({ code: 200, result: 'allow', reason_code: 'verified', display_text: '欢迎光临\n成人票', voice_code: 'welcome', open_duration: 0 }))
  })

  await enterSession(page)
  await page.getByRole('button', { name: '输入票码' }).click()
  await page.getByRole('dialog').getByPlaceholder('输入票码').fill('TICKET-UNKNOWN')
  await page.getByRole('dialog').getByRole('button', { name: '核销' }).click()

  await expect(page.getByText('结果待确认')).toBeVisible()
  await expect(page.getByRole('button', { name: '打开相机扫码' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '输入票码', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '重试' }).click()

  await expect(page.getByText('核销成功')).toBeVisible()
  expect(requestIDs).toHaveLength(2)
  expect(requestIDs[0]).toBe(requestIDs[1])
})

test('刷新后恢复会话和待确认请求', async ({ page }) => {
  const requestIDs: string[] = []
  let attempt = 0
  await page.addInitScript(() => {
    sessionStorage.setItem('mobile_token', 'mobile-staff-token')
    sessionStorage.setItem('mobile_session', 'mobile-session-token')
    sessionStorage.setItem('mobile_session_expires_at', '2099-01-01T00:00:00Z')
    sessionStorage.setItem('mobile_checkpoint_id', '11')
    sessionStorage.setItem('mobile_device_id', '21')
    sessionStorage.setItem('mobile_pending_verification', JSON.stringify({ id: 'restored-request', ticketCode: 'TICKET-RESTORED' }))
  })
  await setupMobileRoutes(page, async route => {
    const body = route.request().postDataJSON() as { request_id: string }
    requestIDs.push(body.request_id)
    attempt += 1
    await route.fulfill(json({ code: 200, result: 'allow', reason_code: 'verified', display_text: '欢迎光临\n成人票', voice_code: 'welcome', open_duration: 0 }))
  })

  await page.goto('/mobile')
  await expect(page.getByTestId('session-restoring')).toBeVisible()
  await expect(page.getByText('结果待确认')).toBeVisible()
  await expect(page.getByRole('button', { name: '打开相机扫码' })).toBeDisabled()
  await expect(page.getByRole('button', { name: /当前检票点/ })).toBeDisabled()
  await expect(page.getByRole('button', { name: '重试' })).toBeVisible()
  await page.getByRole('button', { name: '重试' }).click()

  await expect(page.getByText('核销成功')).toBeVisible()
  expect(attempt).toBe(1)
  expect(requestIDs).toEqual(['restored-request'])
})

test('渠道确认中的业务响应进入待确认状态', async ({ page }) => {
  const requestIDs: string[] = []
  await setupMobileRoutes(page, async route => {
    const body = route.request().postDataJSON() as { request_id: string }
    requestIDs.push(body.request_id)
    await route.fulfill(json({ code: 409, result: 'deny', reason_code: 'processing', display_text: '核销确认中，请稍后重试' }))
  })

  await enterSession(page)
  await page.getByRole('button', { name: '输入票码' }).click()
  await page.getByRole('dialog').getByPlaceholder('输入票码').fill('XHS-PENDING')
  await page.getByRole('dialog').getByRole('button', { name: '核销' }).click()

  await expect(page.getByText('结果待确认')).toBeVisible()
  await expect(page.getByRole('button', { name: '打开相机扫码' })).toBeDisabled()
  expect(requestIDs).toHaveLength(1)
})
