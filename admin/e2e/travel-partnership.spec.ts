import { expect, test, type Page } from '@playwright/test'

async function mockJSON(page: Page, pattern: string, body: unknown) {
  await page.route(pattern, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  }))
}

const tenantUser = (capability: 'supplier' | 'travel_agency') => ({
  id: 1,
  username: 'admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: capability === 'supplier' ? 1 : 2,
  tenant_name: capability === 'supplier' ? '示例景区' : '示例旅行社',
  system_code: capability === 'supplier' ? 'SCENIC001' : 'TRAVEL001',
  capabilities: [{ capability, status: 'active' }],
})

async function openTeamWorkspace(page: Page, capability: 'supplier' | 'travel_agency') {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'tenant-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, tenantUser(capability))
  await mockJSON(page, '**/api/v1/teams?*', { data: [], total: 0 })
  await page.goto('/teams')
  await expect(page.getByRole('heading', { name: '团队业务' })).toBeVisible()
}

test('纯旅行社可以在团队工作台申请合作景区', async ({ page }) => {
  let application: Record<string, unknown> | undefined
  await mockJSON(page, '**/api/v1/teams/partners/suppliers', { data: [] })
  await mockJSON(page, '**/api/v1/teams/partners/supplier-search?*', {
    data: { supplier_tenant_id: 1, supplier_name: '示例景区', supplier_code: 'SCENIC001', contact: '景区联系人', status: '' },
  })
  await page.route('**/api/v1/teams/partners/suppliers', async route => {
    if (route.request().method() === 'POST') {
      application = route.request().postDataJSON()
      await route.fulfill({ status: 201, contentType: 'application/json', body: '{"status":"pending"}' })
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{"data":[]}' })
  })

  await openTeamWorkspace(page, 'travel_agency')
  await page.getByRole('tab', { name: '合作景区' }).click()
  await page.getByRole('button', { name: '申请合作景区' }).click()
  const dialog = page.getByRole('dialog', { name: '申请合作景区' })
  await dialog.getByPlaceholder('输入景区提供的系统编号').fill('SCENIC001')
  await dialog.getByRole('button', { name: '查询' }).click()
  await expect(dialog.getByText('示例景区')).toBeVisible()
  await dialog.getByRole('button', { name: '提交合作申请' }).click()

  await expect.poll(() => application).toEqual({ system_code: 'SCENIC001' })
  await expect(page.getByText('组合产品', { exact: true })).toHaveCount(0)
})

test('供应商审核旅行社后可直接创建合同', async ({ page }) => {
  let relationshipStatus = 'pending'
  let auditBody: Record<string, unknown> | undefined
  await page.route('**/api/v1/teams/partners/travel-agencies', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ relationship_id: 8, travel_tenant_id: 2, travel_name: '示例旅行社', travel_code: 'TRAVEL001', contact: '旅行社联系人', phone: '13800138000', status: relationshipStatus, created_at: '2026-08-08T10:00:00Z' }] }),
  }))
  await page.route('**/api/v1/teams/partners/travel-agencies/8/audit', async route => {
    auditBody = route.request().postDataJSON()
    relationshipStatus = 'active'
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{"status":"active"}' })
  })
  await mockJSON(page, '**/api/v1/teams/contract-partners', { data: [{ tenant_id: 2, name: '示例旅行社', system_code: 'TRAVEL001', relationship_id: 8 }] })
  await mockJSON(page, '**/api/v1/products?*', { data: [{ id: 3, name: '团队票', status: 'online', is_distributable: true }], total: 1 })

  await mockJSON(page, '**/api/v1/teams/contract-products', { data: [{ id: 3, name: '团队票', scenic_area_id: 1, scenic_area_name: '示例景区' }] })
  await openTeamWorkspace(page, 'supplier')
  await page.getByRole('tab', { name: '合作旅行社' }).click()
  await expect(page.getByText('示例旅行社')).toBeVisible()
  await page.getByRole('button', { name: '通过' }).click()
  await page.getByRole('button', { name: '确认通过' }).click()
  await expect.poll(() => auditBody).toEqual({ status: 'active' })
  await page.getByRole('button', { name: '创建合同' }).click()

  const contractDialog = page.getByRole('dialog', { name: '新增旅行社合同' })
  await expect(contractDialog).toBeVisible()
  await expect(contractDialog.getByRole('combobox').first()).toBeDisabled()
})
