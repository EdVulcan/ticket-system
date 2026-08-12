export type TenantIdentity = Record<string, any>

const REFRESH_CACHE_MS = 2_000
let lastRefreshAt = 0
let refreshPromise: Promise<TenantIdentity> | null = null

export const readStoredUser = (): TenantIdentity => {
  try {
    return JSON.parse(localStorage.getItem('user') || '{}')
  } catch {
    return {}
  }
}

export const capabilityIsActive = (item: any, now = Date.now()) => {
  if (item?.status !== 'active') return false
  if (!item.expires_at) return true
  const expiresAt = new Date(item.expires_at).getTime()
  return Number.isFinite(expiresAt) && expiresAt > now
}

export const activeCapabilitySet = (user: TenantIdentity) => new Set<string>(
  (user.capabilities || []).filter((item: any) => capabilityIsActive(item)).map((item: any) => item.capability)
)

export const configuredCapabilitySet = (user: TenantIdentity) => new Set<string>(
  (user.capabilities || [])
    .filter((item: any) => item.status === 'active' || item.status === 'suspended')
    .map((item: any) => item.capability)
)

export const activeSupplierBusinessTypeSet = (user: TenantIdentity) => new Set<string>(
  (user.supplier_business_types || [])
    .filter((item: any) => item.status === 'active')
    .map((item: any) => item.business_type)
)

export const configuredSupplierBusinessTypeSet = (user: TenantIdentity) => new Set<string>(
  (user.supplier_business_types || []).map((item: any) => item.business_type)
)

export const isActiveScenicSupplier = (user: TenantIdentity) => (
  activeCapabilitySet(user).has('supplier') && activeSupplierBusinessTypeSet(user).has('scenic')
)

// A suspended scenic vertical must keep access to its historical orders and reports.
export const isScenicHistorySupplier = (user: TenantIdentity) => (
  activeCapabilitySet(user).has('supplier') && configuredSupplierBusinessTypeSet(user).has('scenic')
)

const mergeTenantIdentity = (current: TenantIdentity, tenant: TenantIdentity): TenantIdentity => ({
  ...current,
  tenant_id: tenant.id ?? current.tenant_id,
  tenant_name: tenant.name ?? current.tenant_name,
  system_code: tenant.system_code ?? current.system_code,
  tenant_status: tenant.status ?? current.tenant_status,
  capabilities: Array.isArray(tenant.capabilities) ? tenant.capabilities : current.capabilities,
  supplier_business_types: Array.isArray(tenant.supplier_business_types)
    ? tenant.supplier_business_types
    : current.supplier_business_types,
})

export const refreshStoredTenantIdentity = async (force = false): Promise<TenantIdentity> => {
  const current = readStoredUser()
  const token = localStorage.getItem('token')
  if (!token || current.scope !== 'tenant') return current
  if (!force && Date.now() - lastRefreshAt < REFRESH_CACHE_MS) return current
  if (refreshPromise) return refreshPromise

  refreshPromise = (async () => {
    try {
      const baseURL = String(import.meta.env.VITE_API_URL || '/api/v1').replace(/\/$/, '')
      const response = await fetch(`${baseURL}/tenants/me`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (response.status === 401) {
        localStorage.removeItem('token')
        localStorage.removeItem('user')
        return {}
      }
      if (!response.ok) return current
      const tenant = await response.json()
      const next = mergeTenantIdentity(current, tenant)
      localStorage.setItem('user', JSON.stringify(next))
      lastRefreshAt = Date.now()
      window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: next }))
      return next
    } catch {
      return current
    } finally {
      refreshPromise = null
    }
  })()

  return refreshPromise
}
