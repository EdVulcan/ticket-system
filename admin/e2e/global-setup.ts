import { request, type FullConfig } from '@playwright/test'

const backendURL = 'http://127.0.0.1:19180/api/v1/'
const tenants = [
  { name: 'E2E 分销商', system_code: 'E2EDIST', capability: 'distributor', password: 'Distributor-E2E-Password-3' },
  { name: 'E2E 旅行社', system_code: 'E2ETRAVEL', capability: 'travel_agency', password: 'Travel-E2E-Password-4' },
]

export default async function globalSetup(_config: FullConfig) {
  const api = await request.newContext({ baseURL: backendURL })
  const login = await api.post('auth/platform/login', {
    data: { username: 'platform-e2e', password: 'Platform-E2E-Password-2' },
  })
  if (!login.ok()) throw new Error(`platform bootstrap login failed: ${login.status()} ${await login.text()}`)
  const { token } = await login.json()
  const headers = { Authorization: `Bearer ${token}` }

  for (const tenant of tenants) {
    const created = await api.post('tenants', {
      headers,
      data: {
        name: tenant.name,
        system_code: tenant.system_code,
        admin_username: 'admin',
        admin_password: tenant.password,
      },
    })
    if (!created.ok()) throw new Error(`create ${tenant.system_code} failed: ${created.status()} ${await created.text()}`)
    const row = await created.json()
    const lifecycle = await api.patch(`tenants/${row.id}/lifecycle`, {
      headers,
      data: { qualification_status: 'approved', qualification_no: `E2E-${row.id}`, reason: 'automated browser test fixture' },
    })
    if (!lifecycle.ok()) throw new Error(`approve ${tenant.system_code} failed: ${lifecycle.status()} ${await lifecycle.text()}`)
    const capability = await api.put(`tenants/${row.id}/capabilities/${tenant.capability}`, {
      headers,
      data: { status: 'active', reason: 'automated browser test fixture' },
    })
    if (!capability.ok()) throw new Error(`enable ${tenant.system_code} failed: ${capability.status()} ${await capability.text()}`)
    const activated = await api.patch(`tenants/${row.id}/status`, { headers, data: { status: 'active' } })
    if (!activated.ok()) throw new Error(`activate ${tenant.system_code} failed: ${activated.status()} ${await activated.text()}`)
  }
  await api.dispose()
}
