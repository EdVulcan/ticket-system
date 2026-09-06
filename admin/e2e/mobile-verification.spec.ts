import { expect, test } from '@playwright/test'

const json = (body: unknown, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })

test('手机核销登录、选点和手动票码回退可用', async ({ page }) => {
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
  await page.route('**/api/v1/mobile/session/verify', route => route.fulfill(json({
    code: 200, result: 'allow', display_text: '欢迎光临\n成人票', voice_code: 'welcome', open_duration: 5000,
  })))

  await page.goto('/mobile')
  const mobilePage = page.locator('.mobile-verify-page')
  await mobilePage.getByLabel('系统编号').fill('SYS001')
  await mobilePage.getByLabel('员工工号').fill('1001')
  await mobilePage.getByLabel('密码').fill('password')
  await page.getByRole('button', { name: '登录并开始' }).click()
  await expect(page.getByRole('heading', { name: '选择核销点位' })).toBeVisible()
  await page.getByRole('button', { name: '进入核销' }).click()
  await expect(page.getByText('打开相机扫码')).toBeVisible()

  await page.getByRole('button', { name: '手动输入' }).click()
  await page.getByPlaceholder('输入票码').fill('TICKET-001')
  await page.getByRole('button', { name: '核销' }).click()
  await expect(page.getByText('核销成功')).toBeVisible()
  await expect(page.getByText('欢迎光临', { exact: false })).toBeVisible()
})
