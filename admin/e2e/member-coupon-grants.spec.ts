import { expect, test, type Page, type Route } from '@playwright/test'
const admin = { id: 21, tenant_id: 21, scope: 'tenant', role: 'admin', username: 'coupon-admin', customer_membership_enabled: true, capabilities: [], business_capabilities: [{ business_type: 'restaurant', status: 'active' }] }
const members = [
  { id: 1, member_no: 'M001', display_name: '小明', status: 'active', membership_status: 'active' },
  { id: 2, member_no: 'M002', display_name: '小红', status: 'active', membership_status: 'active' },
  { id: 3, member_no: 'M003', display_name: '待认证客户', status: 'active', membership_status: 'provisional' },
  { id: 4, member_no: 'M004', display_name: '冻结会员', status: 'frozen', membership_status: 'active' },
]
const template = { id: 31, channel_account_id: 11, name: '会员回馈券', discount_cents: 500, min_goods_subtotal_cents: 2000, valid_days: 7, status: 'active', business_types: ['restaurant'] }
async function json(route: Route, data: any, status = 200) { await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }) }
async function setup(page: Page, options: { user?: any; deny?: boolean; unknown?: boolean; conflict?: boolean; refreshFailure?: boolean; retryForbidden?: boolean; generic400?: boolean; mismatch?: boolean; rejected?: boolean } = {}) {
  const user = options.user || admin
  const posts: any[] = [], previews: any[] = [], queries: string[] = []
  let committed: any = null
  await page.addInitScript(u => { localStorage.setItem('token', 'coupon-test'); localStorage.setItem('user', JSON.stringify(u)) }, user)
  await page.route('**/api/v1/**', async route => {
    const req = route.request(), url = new URL(req.url()), path = url.pathname
    if (path.endsWith('/tenants/me')) return json(route, { ...user, status: 'active' })
    if (path.endsWith('/members/benefit')) return json(route, { configured: false })
    if (path.endsWith('/members')) {
      if (committed && options.refreshFailure) return json(route, { error: '列表暂时不可用' }, 503)
      const keyword = url.searchParams.get('keyword') || ''
      return json(route, { items: members.filter(m => m.display_name.includes(keyword) || m.member_no.includes(keyword)), total: members.length })
    }
    if (path.endsWith('/storefront-channels')) return json(route, { data: [{ id: 11, code: '会员店铺', status: 'active' }, { id: 12, code: '另一店铺', status: 'active' }] })
    if (path.endsWith('/coupon-templates')) return json(route, { data: url.searchParams.get('channel_account_id') === '12' ? [{ ...template, id: 32, channel_account_id: 12, name: '另一店铺券' }] : [template] })
    if (path.endsWith('/member-coupon-grants/preview')) {
      const body = req.postDataJSON(); previews.push(body)
      return json(route, { eligible: !options.deny, reason: options.deny ? '会员未关联该店铺' : '', template, member_count: body.member_ids.length, recipients: body.member_ids.map((id: number) => ({ ...members.find(m => m.id === id), member_id: id, eligible: !options.deny, reason: options.deny ? '会员未关联该店铺' : '' })) })
    }
    if (path.endsWith('/member-coupon-grants') && req.method() === 'POST') {
      const body = req.postDataJSON(); posts.push(body)
      committed = { status: 'completed', ...body, channel_account_id: 11, member_count: body.member_ids.length, items: body.member_ids.map((id: number) => ({ ...members.find(m => m.id === id), member_id: id, grant_id: id + 50 })) }
      if (options.rejected && posts.length === 1) return json(route, { error: '本次资格已变化，请重新检查', reason_code: 'MEMBER_COUPON_GRANT_INELIGIBLE' }, 400)
      if (options.retryForbidden && posts.length > 1) return json(route, { error: '发放权限已停用', reason_code: 'MEMBER_COUPON_GRANT_FORBIDDEN' }, 403)
      if (options.generic400 && posts.length === 1) return json(route, { error: '请求暂时无法处理' }, 400)
      if (options.mismatch && posts.length === 1) return json(route, { ...committed, idempotency_key: 'wrong-attempt' })
      if (options.unknown && posts.length === 1) return route.abort('failed')
      if (options.conflict && posts.length === 1) return json(route, { error: '请查询原发放记录', reason_code: 'MEMBER_COUPON_GRANT_CONFLICT' }, 409)
      return json(route, committed)
    }
    if (path.includes('/member-coupon-grants/')) { queries.push(path.split('/').pop()!); return committed ? json(route, committed) : json(route, { error: '未找到' }, 404) }
    return json(route, { data: [], unread_count: 0 })
  })
  return { posts, previews, queries }
}
const dialog = (page: Page) => page.getByRole('dialog', { name: '发优惠券给会员' })
async function openSingle(page: Page) { await page.goto('/members'); await page.getByRole('row').filter({ hasText: 'M001' }).getByRole('button', { name: '发优惠券', exact: true }).click() }
async function selectTemplate(page: Page, name = '会员回馈券') {
  await dialog(page).getByTestId('grant-template-select').click()
  await page.getByRole('option', { name, exact: true }).click()
}
async function preview(page: Page) { await dialog(page).getByRole('button', { name: '检查发放条件', exact: true }).click(); await expect(dialog(page).getByRole('button', { name: '确认发放', exact: true })).toBeEnabled() }

test('会员单发：预览无副作用，用途选填，确认后每人一张', async ({ page }) => {
  const { posts, previews } = await setup(page)
  await openSingle(page); await selectTemplate(page)
  await expect(dialog(page).getByText('减 ¥5.00 · 商品金额满 ¥20.00 可用')).toBeVisible()
  await preview(page); expect(posts).toHaveLength(0)
  expect(previews[0]).toEqual({ template_id: 31, member_ids: [1] })
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
  expect(posts[0]).toMatchObject({ template_id: 31, member_ids: [1], reason: '' })
})

test('会员仅可勾选正式正常会员批量发券', async ({ page }) => {
  const { posts } = await setup(page); await page.goto('/members')
  for (const no of ['M001', 'M002']) await page.getByRole('row').filter({ hasText: no }).locator('.el-checkbox').click()
  await expect(page.getByRole('row').filter({ hasText: 'M003' }).getByRole('checkbox')).toBeDisabled()
  await expect(page.getByRole('row').filter({ hasText: 'M004' }).getByRole('checkbox')).toBeDisabled()
  await page.getByRole('button', { name: '批量发券（2/100）', exact: true }).click(); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect.poll(() => posts.length).toBe(1)
  expect(posts[0].member_ids).toEqual([1, 2])
})

test('营销模板入口锁定店铺模板并选择会员', async ({ page }) => {
  const { posts } = await setup(page); await page.goto('/commerce/marketing')
  await page.getByRole('button', { name: '发给会员', exact: true }).click()
  await expect(dialog(page).getByRole('combobox', { name: /优惠券模板/ })).toBeDisabled()
  await expect(dialog(page).getByRole('combobox', { name: /店铺渠道/ })).toBeDisabled()
  await dialog(page).getByRole('checkbox', { name: '选择会员M001', exact: true }).locator('..').click()
  await preview(page); await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect.poll(() => posts.length).toBe(1)
  expect(posts[0].template_id).toBe(31)
})

for (const user of [{ ...admin, role: 'product_operator', permissions: ['catalog.read', 'catalog.write', 'members.read'] }, { ...admin, role: 'viewer', permissions: ['catalog.read', 'members.read'] }, { ...admin, customer_membership_enabled: false }, { ...admin, business_capabilities: [{ business_type: 'restaurant', status: 'suspended' }] }]) test(`发券权限关闭 ${user.role} ${user.customer_membership_enabled} ${user.business_capabilities.length}`, async ({ page }) => {
  const { posts, previews } = await setup(page, { user }); await page.goto('/commerce/marketing')
  await expect(page.getByRole('heading', { name: '营销中心' })).toBeVisible()
  await expect(page.getByRole('button', { name: '发给会员', exact: true })).toHaveCount(0)
  expect(posts).toHaveLength(0); expect(previews).toHaveLength(0)
})

test('预览不符合条件时不可发放', async ({ page }) => {
  const { posts } = await setup(page, { deny: true }); await openSingle(page); await selectTemplate(page)
  await dialog(page).getByRole('button', { name: '检查发放条件', exact: true }).click()
  await expect(dialog(page).getByText('会员未关联该店铺').first()).toBeVisible()
  await expect(dialog(page).getByRole('button', { name: '确认发放', exact: true })).toBeDisabled()
  expect(posts).toHaveLength(0)
})

test('店铺切换忽略旧模板响应，不能发旧模板', async ({ page }) => {
  await setup(page); await openSingle(page); await selectTemplate(page)
  let release!: () => void
  const wait = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/commerce/promotions/coupon-templates**', async route => {
    const id = new URL(route.request().url()).searchParams.get('channel_account_id')
    if (id === '12') await wait
    await json(route, { data: [{ ...template, channel_account_id: Number(id), id: id === '12' ? 32 : 31, name: id === '12' ? '过期响应店铺券' : template.name }] })
  })
  await dialog(page).getByTestId('grant-channel-select').click(); await page.getByRole('option', { name: '另一店铺', exact: true }).click()
  await dialog(page).getByTestId('grant-channel-select').click(); await page.getByRole('option', { name: '会员店铺', exact: true }).click()
  release(); await selectTemplate(page)
  await expect(dialog(page).getByText('过期响应店铺券')).toHaveCount(0)
  await preview(page)
})

test('确认双击只提交一次，成功后列表加载失败不变成发放失败', async ({ page }) => {
  const { posts } = await setup(page, { refreshFailure: true }); await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).dblclick()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
  await dialog(page).getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByText('列表暂时不可用')).toBeVisible()
  expect(posts).toHaveLength(1)
  expect(await page.evaluate(() => Object.keys(sessionStorage).filter(key => key.startsWith('member-coupon-grant:')))).toEqual([])
})

for (const conflict of [false, true]) test(`提交结果丢失后刷新恢复原记录 ${conflict ? '冲突查询' : '原键重试'}`, async ({ page }) => {
  const { posts, queries } = await setup(page, { unknown: !conflict, conflict }); await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByRole('button', { name: '重试原发放', exact: true })).toBeEnabled()
  await expect(dialog(page).getByRole('combobox', { name: /店铺渠道/ })).toHaveCount(0)
  const original = posts[0]
  await dialog(page).getByRole('button', { name: '关闭', exact: true }).click()
  if (conflict) await page.goto('/commerce/marketing')
  await page.reload()
  await page.getByRole('button', { name: '恢复待确认发券', exact: true }).click()
  if (conflict) {
    await dialog(page).getByRole('button', { name: '查询发放结果', exact: true }).click()
    await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
    expect(queries).toContain(original.idempotency_key); expect(posts).toHaveLength(1)
  } else {
    await dialog(page).getByRole('button', { name: '重试原发放', exact: true }).click()
    await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
    expect(posts).toHaveLength(2); expect(posts[1]).toEqual(original)
  }
})

test('首次结果丢失后重试被拒绝仍保留原记录，刷新后可查询成功', async ({ page }) => {
  const { posts, queries } = await setup(page, { unknown: true, retryForbidden: true })
  await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await dialog(page).getByRole('button', { name: '重试原发放', exact: true }).click()
  await expect.poll(() => posts.length).toBe(2)
  expect(posts[1]).toEqual(posts[0])
  await expect(dialog(page).getByRole('button', { name: '查询发放结果', exact: true })).toBeEnabled()
  await page.reload(); await page.getByRole('button', { name: '恢复待确认发券', exact: true }).click()
  await dialog(page).getByRole('button', { name: '查询发放结果', exact: true }).click()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
  expect(queries).toContain(posts[0].idempotency_key)
})
for (const options of [{ generic400: true }, { mismatch: true }]) test(`非确定拒绝或错配回执保持原发放 ${JSON.stringify(options)}`, async ({ page }) => {
  const { posts } = await setup(page, options); await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByRole('button', { name: '重试原发放', exact: true })).toBeEnabled()
  expect(posts).toHaveLength(1)
  await dialog(page).getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.getByRole('button', { name: '恢复待确认发券', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '恢复待确认发券', exact: true }).click()
  await dialog(page).getByRole('button', { name: '查询发放结果', exact: true }).click()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
})

test('确定未受理可重新检查后发放新记录', async ({ page }) => {
  const { posts } = await setup(page, { rejected: true }); await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByText('本次资格已变化，请重新检查')).toBeVisible()
  await expect(dialog(page).getByRole('button', { name: '重试原发放', exact: true })).toHaveCount(0)
  await preview(page); await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
  expect(posts).toHaveLength(2)
  expect(posts[1].idempotency_key).not.toBe(posts[0].idempotency_key)
})
test('查询尚未提交完成不能解除锁定，可用原记录重试', async ({ page }) => {
  const { posts } = await setup(page, { unknown: true }); await openSingle(page); await selectTemplate(page); await preview(page)
  await dialog(page).getByRole('button', { name: '确认发放', exact: true }).click()
  await expect(dialog(page).getByRole('button', { name: '重试原发放', exact: true })).toBeEnabled()
  await page.route('**/api/v1/commerce/promotions/member-coupon-grants/*', async route => {
    if (route.request().method() === 'GET') await json(route, { error: '尚无记录' }, 404)
    else await route.fallback()
  })
  await dialog(page).getByRole('button', { name: '查询发放结果', exact: true }).click()
  await expect(dialog(page).getByText('暂未查到已完成记录，可继续查询或重试原发放。')).toBeVisible()
  await dialog(page).getByRole('button', { name: '重试原发放', exact: true }).click()
  await expect(dialog(page).getByText('优惠券已发放', { exact: true })).toBeVisible()
  expect(posts[1]).toEqual(posts[0])
})
