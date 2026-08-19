import { expect, test, type Page, type Route } from '@playwright/test'

const supplierUser = {
  id: 8,
  username: 'supplier_admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '青云景区',
  system_code: 'QY001',
  permissions: ['catalog.read', 'catalog.write'],
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

test('供应商可以预览、保存、发布、查看历史并停用打印模板', async ({ page }) => {
  await page.addInitScript(user => {
    localStorage.setItem('token', 'supplier-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, supplierUser)
  await page.route('**/api/v1/tenants/me', route => json(route, {
    id: supplierUser.tenant_id,
    name: supplierUser.tenant_name,
    system_code: supplierUser.system_code,
    status: 'active',
    capabilities: supplierUser.capabilities,
    supplier_business_types: supplierUser.supplier_business_types,
  }))
  await page.route('**/api/v1/scenic-areas*', route => json(route, { data: [{ id: 11, name: '青云景区', code: 'QY-SCENIC', status: 'active' }] }))
  await page.route('**/api/v1/products?*', route => json(route, { data: [{ id: 101, name: '青云成人票', scenic_area_id: 11, current_revision_id: 201, product_kind: 'ticket', status: 'online' }], total: 1 }))

  let template = {
    id: 501,
    tenant_id: 1,
    scenic_area_id: 11,
    product_id: 0,
    product_revision_id: 0,
    name: '窗口标准票据',
    status: 'active',
    current_revision_id: 0,
    paper_width_mm: 58,
    printer_profile: 'escpos',
    draft_revision: null as any,
    current_revision: null as any,
    definition: null as any,
    orientation: 'portrait',
  }
  let nextRevision = 1
  await page.route('**/api/v1/print-templates**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    if (path.endsWith('/preview') && request.method() === 'POST') {
      const body = request.postDataJSON()
      await json(route, {
        definition: body.definition,
        document: { schema_version: 1, paper_width_mm: body.paper_width_mm || 58, orientation: body.orientation || 'portrait', template_name: body.name || '样例模板', scenic_area: '青云景区', blocks: (body.definition?.blocks || []).map((block: any) => ({ ...block, text: block.kind === 'ticket_code' ? '票码：SAMPLE-001' : block.text || block.kind })) },
        content_hash: 'preview-hash-501',
      })
      return
    }
    if (path.endsWith('/revisions') && request.method() === 'GET') {
      await json(route, { data: template.current_revision ? [template.current_revision, template.draft_revision].filter(Boolean) : [] })
      return
    }
    if (path.endsWith('/publish') && request.method() === 'POST') {
      template.current_revision_id = template.draft_revision?.id || 701
      template.current_revision = { ...(template.draft_revision || {}), id: template.current_revision_id, version: nextRevision++, status: 'published', published_at: '2026-08-19T10:00:00+08:00' }
      template.draft_revision = null
      await json(route, template)
      return
    }
    if (path.endsWith('/status') && request.method() === 'PATCH') {
      template.status = request.postDataJSON().status
      await json(route, template)
      return
    }
    if (request.method() === 'POST' || request.method() === 'PUT') {
      const body = request.postDataJSON()
      template.name = body.name
      template.paper_width_mm = body.paper_width_mm
      template.orientation = body.orientation || 'portrait'
      template.draft_revision = { id: 701, version: nextRevision, status: 'draft', definition_hash: 'draft-hash-501', definition: body.definition, created_at: '2026-08-19T10:00:00+08:00' }
      await json(route, template, request.method() === 'POST' ? 201 : 200)
      return
    }
    await json(route, { data: [template] })
  })

  await page.goto('/print-templates')
  await expect(page.getByRole('heading', { name: '门票打印模板' })).toBeVisible()
  await page.getByRole('button', { name: '新建模板' }).click()
  const editor = page.getByRole('dialog', { name: '新建打印模板' })
  await editor.getByRole('textbox', { name: '模板名称' }).fill('窗口标准票据')
  await editor.getByText('横版', { exact: true }).click()
  await expect(editor.locator('.paper-landscape')).toBeVisible()
  await editor.getByText('80 mm', { exact: true }).click()
  await expect(editor.locator('.paper-wide.paper-landscape')).toBeVisible()
  await editor.getByRole('button', { name: '增加打印区块' }).click()
  await expect(editor.getByText('只允许结构化字段；不支持任意 HTML、JavaScript、SQL 或渠道密钥。')).toBeVisible()
  await editor.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('打印模板草稿已保存')).toBeVisible()

  await page.getByRole('button', { name: '发布' }).click()
  const confirm = page.getByRole('dialog', { name: '发布打印模板' })
  await confirm.getByRole('button', { name: '确定' }).click()
  await expect(page.getByText('打印模板已发布')).toBeVisible()

  await page.getByRole('button', { name: '版本' }).click()
  await expect(page.getByRole('dialog', { name: '模板版本历史' })).toBeVisible()
  await page.getByRole('button', { name: '关闭' }).click().catch(() => undefined)

  await page.getByRole('button', { name: '停用' }).click()
  await expect(page.getByText('模板已停用')).toBeVisible()
})
