export const tenantRoleOptions = [
  { value: 'admin', label: '商户管理员', description: '管理本商户全部业务和账号' },
  { value: 'product_operator', label: '产品与分销运营', description: '维护产品、分销和渠道，查看订单与报表' },
  { value: 'team_operator', label: '团队业务员', description: '维护旅行社合同、团队、名单和现场确认' },
  { value: 'settlement_operator', label: '结算对账员', description: '处理上下游对账、结算、退款待办和报表' },
  { value: 'viewer', label: '只读查看', description: '查看授权业务数据，不可新增、修改或删除' },
]

export const tenantRoleLabel = (role: string) => tenantRoleOptions.find(item => item.value === role)?.label || ({
  super_admin: '商户最高管理员', seller: '售票员', checker: '验票员',
} as Record<string, string>)[role] || '系统用户'

export const hasPermission = (user: any, permission: string) => {
  if (user?.scope !== 'tenant') return false
  if (user?.role === 'admin' || user?.role === 'super_admin') return true
  return Array.isArray(user?.permissions) && (user.permissions.includes('*') || user.permissions.includes(permission))
}
