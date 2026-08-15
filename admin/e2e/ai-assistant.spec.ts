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

test('AI 助手预览明确区分窗口票类型和未上架状态', async ({ page }) => {
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
      await json(route, {
        task_id: 42,
        operation_type: 'ticket_product_create',
        state: 'awaiting_confirmation',
        input_text: '创建窗口成人票，售价 120 元，结算价 80 元',
        can_confirm: true,
        preview: {
          operation_type: 'ticket_product_create',
          scenic_area_name: '示例景区',
          product: {
            name: '窗口成人票',
            type: 'offline',
            type_label: '窗口/POS 票',
            status: 'offline',
            status_label: '未上架',
            is_distributable: false,
            price: 120,
            settlement_price: 80,
            validity_type: 'date',
            stock_type: 'unlimited',
            real_name_required: false,
            limit_per_phone: 0,
            limit_per_id: 0,
            refund_type: 'no_refund',
            code_mode: 'order',
            gate_voice_code: 'welcome',
          },
          rule: { name: '窗口成人票', validity_type: 'date', groups: [] },
          rule_groups: [],
          assumptions: [],
          safety: [],
        },
      })
      return
    }
    if (url.endsWith('/agent/tasks/42/confirm')) {
      await json(route, { task_id: 42, state: 'completed', can_confirm: false, result: { product_id: 99 } })
      return
    }
    await json(route, {})
  })

  await page.goto('/')
  const assistant = page.getByTestId('ai-assistant')
  await assistant.getByRole('button', { name: '打开 AI 助手' }).click()
  await assistant.getByLabel('操作描述').fill('创建窗口成人票，售价 120 元，结算价 80 元')
  await assistant.getByRole('button', { name: '生成计划' }).click()

  await expect(assistant.getByText('窗口/POS 票', { exact: true })).toBeVisible()
  await expect(assistant.getByText('未上架 · 不分销', { exact: true })).toBeVisible()
  await expect(assistant.getByRole('button', { name: '确认执行' })).toBeEnabled()
})

test('AI 助手可放弃过期任务并从新任务开始', async ({ page }) => {
  let newTaskBody: Record<string, unknown> | null = null
  await page.addInitScript(user => {
    localStorage.setItem('token', 'tenant-token')
    localStorage.setItem('user', JSON.stringify(user))
    localStorage.setItem('ticket-agent-task-id', '77')
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
    if (url.endsWith('/agent/tasks/77') && route.request().method() === 'GET') {
      await json(route, {
        task_id: 77,
        state: 'expired',
        input_text: '创建一个已过期的票种任务',
        error_message: 'agent task expired; start a new task',
        can_confirm: false,
        missing_fields: [{ field: 'price', label: '售价', question: '请提供票面售价。' }],
      })
      return
    }
    if (url.endsWith('/agent/tasks/77/cancel') && route.request().method() === 'POST') {
      await json(route, { task_id: 77, state: 'cancelled', can_confirm: false })
      return
    }
    if (url.endsWith('/agent/tasks') && route.request().method() === 'POST') {
      newTaskBody = route.request().postDataJSON() as Record<string, unknown>
      await json(route, {
        task_id: 78,
        state: 'collecting',
        input_text: '创建一个线上成人票',
        missing_fields: [{ field: 'price', label: '售价', question: '请提供票面售价。' }],
        can_confirm: false,
        availability: { enabled: true, provider: 'deepseek', requests_remaining: 98 },
      })
      return
    }
    await json(route, {})
  })

  await page.goto('/')
  const assistant = page.getByTestId('ai-assistant')
  await assistant.getByRole('button', { name: '打开 AI 助手' }).click()
  await expect(assistant.getByRole('button', { name: '新建任务' })).toBeVisible()
  await assistant.getByRole('button', { name: '新建任务' }).click()
  await page.getByRole('button', { name: '放弃并新建' }).click()

  await expect(assistant.getByRole('button', { name: '生成计划' })).toBeVisible()
  await assistant.getByLabel('操作描述').fill('创建一个线上成人票')
  await assistant.getByRole('button', { name: '生成计划' }).click()
  await expect(assistant.getByText('售价', { exact: true })).toBeVisible()
  expect(newTaskBody?.task_id).toBeUndefined()
  expect(newTaskBody?.idempotency_key).toBeTruthy()
})

test('AI provider 未返回最终答案时提示提高输出 Token', async ({ page }) => {
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
      await json(route, { enabled: true, provider: 'deepseek', requests_remaining: 97 })
      return
    }
    if (url.endsWith('/agent/tasks') && route.request().method() === 'POST') {
      await json(route, { error: 'AI provider did not return a final answer; increase max_output_tokens', code: 'ai_provider_error' }, 502)
      return
    }
    await json(route, {})
  })

  await page.goto('/')
  const assistant = page.getByTestId('ai-assistant')
  await assistant.getByRole('button', { name: '打开 AI 助手' }).click()
  await assistant.getByLabel('操作描述').fill('创建一个线上成人票')
  await assistant.getByRole('button', { name: '生成计划' }).click()
  await expect(assistant.getByRole('alert')).toContainText('提高“最大输出 Token”')
})
