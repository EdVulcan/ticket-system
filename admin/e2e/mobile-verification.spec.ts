import { expect, test, type Page, type Route } from '@playwright/test'

const json = (body: unknown, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })

type MobileRoutes = {
  preview?: (route: Route) => Promise<void> | void
  operation?: (route: Route) => Promise<void> | void
  recovery?: (route: Route) => Promise<void> | void
}

const setupMobileRoutes = async (page: Page, routes: MobileRoutes = {}) => {
  await page.route('**/api/v1/auth/staff/login', route => route.fulfill(json({ token: 'mobile-staff-token', staff: { id: 7, name: '验票员', job_number: '1001' } })))
  await page.route('**/api/v1/tenants/me', route => route.fulfill(json({ id: 1, name: '测试景区', system_code: 'SYS001' })))
  await page.route('**/api/v1/mobile/targets', route => route.fulfill(json({ checkpoints: [{ id: 11, name: '南门', location: '游客中心', scenic_area_id: 3 }], devices: [{ id: 21, name: '南门手机终端', serial_number: 'WEB-001', type: 'handheld', status: 'offline', check_point_id: 11, scenic_area_id: 3 }] })))
  await page.route('**/api/v1/mobile/sessions', route => route.fulfill(json({ session_token: 'mobile-session-token', expires_at: '2099-01-01T00:00:00Z', checkpoint: { id: 11, name: '南门' }, device: { id: 21, name: '南门手机终端', serial_number: 'WEB-001' } }, 201)))
  await page.route('**/api/v1/mobile/session/heartbeat', route => route.fulfill(json({ status: 'active' })))
  await page.route('**/api/v1/mobile/session/close', route => route.fulfill(json({ status: 'closed' })))
  await page.route('**/api/v1/mobile/session/verification-previews', route => routes.preview ? routes.preview(route) : route.fulfill(json({ preview_id: 'preview-1', expires_at: '2099-01-01T00:01:00Z', product_name: '成人票', code_mode: 'order', batch_allowed: true, max_quantity: 2, point_used: 0, point_remaining: 2, requires_repeat_confirmation: false })))
  await page.route('**/api/v1/mobile/session/verification-operations', route => routes.operation ? routes.operation(route) : route.fulfill(json({ operation_id: 'operation-1', status: 'completed', result: 'allow', reason_code: 'verified', display_text: '欢迎光临\n成人票', quantity: 1, point_remaining: 1, completed_at: '2099-01-01T00:00:00Z' })))
  await page.route('**/api/v1/mobile/verification-operations/*', route => routes.recovery ? routes.recovery(route) : route.fulfill(json({ operation_id: 'restored-operation', status: 'completed', result: 'allow', reason_code: 'verified', display_text: '欢迎光临', quantity: 1, point_remaining: 1, completed_at: '2099-01-01T00:00:00Z' })))
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

const openManual = async (page: Page, code: string) => {
  await page.getByRole('button', { name: '输入票码' }).click()
  await page.getByRole('dialog', { name: '输入票码' }).getByPlaceholder('输入票码').fill(code)
  await page.getByRole('dialog', { name: '输入票码' }).getByRole('button', { name: '读取票券' }).click()
}

test('预检不扣次，确认后才提交核销操作', async ({ page }) => {
  let operationCalls = 0
  await setupMobileRoutes(page, { operation: route => { operationCalls += 1; return route.fulfill(json({ operation_id: 'operation-1', status: 'completed', result: 'allow', display_text: '欢迎光临', quantity: 1, point_remaining: 1 })) } })
  await enterSession(page)
  await openManual(page, 'TICKET-001')
  await expect(page.getByRole('dialog', { name: '确认核销' })).toBeVisible()
  expect(operationCalls).toBe(0)
  await expect(page.getByText('确认后才会计次')).toBeVisible()
  await page.getByTestId('confirm-verification').click()
  await expect(page.getByText('核销成功')).toBeVisible()
  expect(operationCalls).toBe(1)
})

test('共享码确认页允许选择数量并提交所选人数', async ({ page }) => {
  let quantity = 0
  await setupMobileRoutes(page, { preview: route => route.fulfill(json({ preview_id: 'preview-shared', expires_at: '2099-01-01T00:01:00Z', product_name: '家庭票', code_mode: 'order', batch_allowed: true, max_quantity: 3, point_used: 1, point_remaining: 5, requires_repeat_confirmation: false })), operation: async route => { quantity = Number(route.request().postDataJSON().quantity); await route.fulfill(json({ status: 'completed', result: 'allow', display_text: '欢迎光临', quantity, point_remaining: 3 })) } })
  await enterSession(page)
  await openManual(page, 'ORDER-SHARED')
  await page.getByRole('button', { name: '增加核销数量' }).click()
  await page.getByRole('button', { name: '增加核销数量' }).click()
  await expect(page.getByTestId('confirmation-quantity')).toHaveText('3')
  await page.getByTestId('confirm-verification').click()
  await expect(page.getByText('本次核销 3 人')).toBeVisible()
  expect(quantity).toBe(3)
})

test('重复码必须显式确认继续核销', async ({ page }) => {
  let operationBody: any
  await setupMobileRoutes(page, { preview: route => route.fulfill(json({ preview_id: 'preview-repeat', expires_at: '2099-01-01T00:01:00Z', product_name: '成人票', code_mode: 'order', batch_allowed: true, max_quantity: 2, point_used: 1, point_remaining: 1, requires_repeat_confirmation: true, recent_operation: { operation_id: 'previous-operation', quantity: 1, completed_at: '2099-01-01T00:00:00Z' } })), operation: async route => { operationBody = route.request().postDataJSON(); await route.fulfill(json({ status: 'completed', result: 'allow', display_text: '欢迎光临', quantity: 1, point_remaining: 0 })) } })
  await enterSession(page)
  await openManual(page, 'ORDER-REPEAT')
  await expect(page.getByText('该票码刚刚有核销记录')).toBeVisible()
  await expect(page.getByTestId('confirm-verification')).toBeDisabled()
  await page.getByLabel('我确认这是继续核销').check()
  await page.getByTestId('confirm-verification').click()
  await expect(page.getByText('核销成功')).toBeVisible()
  expect(operationBody.continuation_of).toBe('previous-operation')
})

test('刷新后通过原 operation_id 查询并恢复最终结果', async ({ page }) => {
  let recoveryCalls = 0
  await page.addInitScript(() => { sessionStorage.setItem('mobile_token', 'mobile-staff-token'); sessionStorage.setItem('mobile_session', 'mobile-session-token'); sessionStorage.setItem('mobile_session_expires_at', '2099-01-01T00:00:00Z'); sessionStorage.setItem('mobile_checkpoint_id', '11'); sessionStorage.setItem('mobile_device_id', '21'); sessionStorage.setItem('mobile_pending_operation', JSON.stringify({ operation_id: 'restored-operation', preview_id: 'preview-1', ticket_code: 'TICKET-RESTORED', quantity: 1 })) })
  await setupMobileRoutes(page, { recovery: async route => { recoveryCalls += 1; await route.fulfill(json({ operation_id: 'restored-operation', status: 'completed', result: 'allow', display_text: '欢迎光临', quantity: 1, point_remaining: 1 })) } })
  await page.goto('/mobile')
  await expect(page.getByTestId('session-restoring')).toBeVisible()
  await expect(page.getByText('核销成功')).toBeVisible()
  expect(recoveryCalls).toBe(1)
})

test('一票一码始终固定本次核销数量为 1', async ({ page }) => {
  let quantity = 0
  await setupMobileRoutes(page, { preview: route => route.fulfill(json({ preview_id: 'preview-ticket', expires_at: '2099-01-01T00:01:00Z', product_name: '儿童票', code_mode: 'ticket', batch_allowed: false, max_quantity: 1, point_used: 0, point_remaining: 1, requires_repeat_confirmation: false })), operation: async route => { quantity = Number(route.request().postDataJSON().quantity); await route.fulfill(json({ status: 'completed', result: 'allow', display_text: '欢迎光临', quantity, point_remaining: 0 })) } })
  await enterSession(page)
  await openManual(page, 'TICKET-ONE')
  await expect(page.getByText('本次核销 1 人')).toBeVisible()
  await expect(page.getByText('此票码按单张票处理')).toBeVisible()
  await page.getByTestId('confirm-verification').click()
  await expect(page.getByText('核销成功')).toBeVisible()
  expect(quantity).toBe(1)
})
