export type TenantIdentity = Record<string, any>

const REFRESH_CACHE_MS = 2_000
let lastRefreshAt = 0
let lastRefreshToken: string | null = null
let refreshInFlight: { token: string; promise: Promise<TenantIdentity> } | null = null

export const readStoredUser = (): TenantIdentity => {
  try {
    return JSON.parse(localStorage.getItem('user') || '{}')
  } catch {
    return {}
  }
}

// Stable authority snapshot for local view invalidation; it is not authorization.
// Display-name refreshes and reordered lists must not discard an editing draft.
export const tenantAuthorityKey = (user: TenantIdentity): string => {
  user = user || {}
  const grants = (items: any) => (Array.isArray(items) ? items : [])
    .filter((item: any) => item && typeof item === 'object')
    .map((item: any) => JSON.stringify([item.capability ?? item.business_type, item.status, item.expires_at ?? null]))
    .sort()
  return JSON.stringify([
    user.scope, user.id, user.tenant_id, user.role, user.is_initial_admin === true, user.tenant_status || 'active',
    [...(Array.isArray(user.permissions) ? user.permissions : [])].sort(),
    grants(user.capabilities), grants(user.supplier_business_types), grants(user.business_capabilities),
  ])
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

export const activeBusinessCapabilitySet = (user: TenantIdentity, now = Date.now()) => new Set<string>(
  (user.business_capabilities || [])
    .filter((item: any) => capabilityIsActive(item, now))
    .map((item: any) => item.business_type)
)

export const configuredBusinessCapabilitySet = (user: TenantIdentity) => new Set<string>(
  (user.business_capabilities || [])
    .filter((item: any) => item.status === 'active' || item.status === 'suspended')
    .map((item: any) => item.business_type)
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
  customer_membership_enabled: tenant.customer_membership_enabled === true,
  tenant_id: tenant.id ?? current.tenant_id,
  tenant_name: tenant.name ?? current.tenant_name,
  system_code: tenant.system_code ?? current.system_code,
  tenant_status: tenant.status ?? current.tenant_status,
  capabilities: Array.isArray(tenant.capabilities) ? tenant.capabilities : current.capabilities,
  supplier_business_types: Array.isArray(tenant.supplier_business_types)
    ? tenant.supplier_business_types
    : current.supplier_business_types,
  business_capabilities: Array.isArray(tenant.business_capabilities)
    ? tenant.business_capabilities
    : current.business_capabilities,
})

export const refreshStoredTenantIdentity = async (force = false): Promise<TenantIdentity> => {
  const current = readStoredUser()
  const token = localStorage.getItem('token')
  if (!token || current.scope !== 'tenant') return current
  if (!force && token === lastRefreshToken && Date.now() - lastRefreshAt < REFRESH_CACHE_MS) return current
  if (refreshInFlight?.token === token) return refreshInFlight.promise

  const promise = (async () => {
    try {
      const baseURL = String(import.meta.env.VITE_API_URL || '/api/v1').replace(/\/$/, '')
      const response = await fetch(`${baseURL}/tenants/me`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      // A previous session may finish after another merchant has logged in.
      if (localStorage.getItem('token') !== token) return readStoredUser()
      if (response.status === 401) {
        localStorage.removeItem('token')
        localStorage.removeItem('user')
        window.dispatchEvent(new CustomEvent('auth-session-expired'))
        return {}
      }
      if (!response.ok) return readStoredUser()
      const tenant = await response.json()
      if (localStorage.getItem('token') !== token) return readStoredUser()
      // The trusted endpoint may correct stale local tenant metadata for this token.
      const next = mergeTenantIdentity(readStoredUser(), tenant)
      localStorage.setItem('user', JSON.stringify(next))
      lastRefreshToken = token
      lastRefreshAt = Date.now()
      window.dispatchEvent(new CustomEvent('tenant-identity-refreshed', { detail: next }))
      return next
    } catch {
      return readStoredUser()
    }
  })()

  const flight = { token, promise }
  refreshInFlight = flight
  try {
    return await promise
  } finally {
    if (refreshInFlight === flight) refreshInFlight = null
  }
}
