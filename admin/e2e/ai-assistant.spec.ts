import { expect, test, type Route } from '@playwright/test'

const tenantUser = {
  id: 1,
  username: 'admin',
  role: 'super_admin',
  scope: 'tenant',
  tenant_id: 1,
  tenant_name: '示例景区',
  system_code: 'SCENIC001',
  permissions: ['catalog.write'],
  capabilities: [{ capability: 'supplier', status: 'active' }],
  supplier_business_types: [{ business_type: 'scenic', status: 'active' }],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

test('AI 助手请求失败时保留输入并支持重试，且不会重复提交', async ({ page }) => {
  let submitCount = 0
  await page.addInitScript(user => {
    localStorage.setItem('token', 'tenant-token')
    localStorage.setItem('user', JSON.stringify(user))
  }, tenantUser)
  await page.route('**/api/v1/**', async route => {
    const url = route.request().url()
    if (url.endsWith('/tenants/me')) {
      await json(route, {
        id: tenantUser.tenant_id,
        name: tenantUser.tenant_name,
        system_code: tenantUser.system_code,
        status: 'active',
        permissions: tenantUser.permissions,
        capabilities: tenantUser.capabilities,
        supplier_business_types: tenantUser.supplier_business_types,
      })
      return
    }
    if (url.endsWith('/catalog/batch-changes/ai-status')) {
      await json(route, { enabled: true, provider: 'deepseek', requests_remaining: 100 })
      return
    }
    if (url.endsWith('/agent/tasks') && route.request().method() === 'POST') {
      submitCount += 1
      if (submitCount === 1) {
        await new Promise(resolve => setTimeout(resolve, 250))
        await json(route, { error: '请求超时，请稍后重试', code: 'ai_unavailable' }, 504)
        return
      }
      await json(route, {
        task_id: 41,
        state: 'collecting',
        input_text: '创建一个成人票飞车套票',
        missing_fields: [{ field: 'price', label: '售价', question: '请提供票面售价。' }],
        can_confirm: false,
        availability: { enabled: true, provider: 'deepseek', requests_remaining: 99 },
      })
      return
    }
    await json(route, {})
  })

  await page.goto('/')
  const assistant = page.getByTestId('ai-assistant')
  await assistant.getByRole('button', { name: '打开 AI 助手' }).click()
  await expect(assistant.getByText('描述你要完成的操作')).toBeVisible()

  const input = assistant.getByLabel('操作描述')
  const submit = assistant.getByRole('button', { name: '生成计划' })
  await input.fill('创建一个成人票飞车套票')
  await submit.click()
  await expect(submit).toBeDisabled()
  await submit.click({ force: true })
  await expect(assistant.getByRole('alert')).toContainText('模型响应超时')
  await expect(page.locator('.el-message')).toHaveCount(0)
  await expect(input).toHaveValue('创建一个成人票飞车套票')
  expect(submitCount).toBe(1)

  await assistant.getByRole('button', { name: '重试' }).click()
  await expect(assistant.getByText('售价', { exact: true })).toBeVisible()
  expect(submitCount).toBe(2)
})
