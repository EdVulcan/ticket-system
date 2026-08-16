const englishErrorRules: Array<[RegExp, string]> = [
	[/team must complete admission before settlement/i, '团队全部实际入园后才能生成结算单'],
	[/team has no verified admission amount/i, '当前团队没有可用于结算的有效核销金额'],
	[/only an unbound draft team can attach a sales order/i, '只有尚未绑定订单的草稿团队可以绑定订单'],
	[/team roster must match the planned headcount/i, '游客名单人数必须与团队计划人数一致'],
	[/order does not have enough member ticket entitlements/i, '该订单可分配的门票数量不足'],
	[/no unused team ticket is available/i, '当前团队订单没有可供新增游客使用的未核销门票，请先完成补票'],
	[/temporary member changes require a confirmed team/i, '团队确认并出票后才能进行临时人数变更'],
	[/refund or void the member ticket before removal/i, '请先完成该游客门票的退票或作废，再执行减员'],
	[/active travel agency relationship not found|active supplier relationship not found/i, '双方尚未建立有效的团队合作关系'],
	[/supplier tenant is unavailable/i, '景区供应商当前不可用'],
	[/team supplier and visit date are required/i, '请选择景区供应商和游玩日期'],
	[/active travel contract not found|travel contract is not active/i, '当前团队合同无效或不在适用日期内'],
	[/travel agency payment requires a confirmed settlement and proof/i, '双方确认结算单后，旅行社需填写付款凭证'],
	[/only supplier can confirm receipt of a submitted payment/i, '付款凭证提交后，只能由景区确认实际到账'],
	[/tenant activation requires approved, unexpired qualification and contract|tenant qualification or contract is not active/i, '启用前请先确认资质已通过；已填写的资质和合同到期日必须晚于当前时间'],
  [/supplier business type is not active|scenic supplier business is (?:unavailable|not active)/i, '景区票务业态未启用或已暂停'],
  [/scenic supplier tenant is unavailable/i, '景区供应商当前无法开展票务业务'],
  [/an active supplier capability is required before enabling a supplier business type/i, '请先启用该商户的供应商身份，再启用供应业态'],
  [/audit reason is required/i, '必须填写本次变更原因'],
  [/invalid supplier business type status/i, '供应业态状态不正确'],
  [/invalid supplier business type/i, '供应业态类型不正确'],
  [/tenant capability is not active/i, '当前商户对应的业务身份未启用或已过期'],
  [/(?:distribution|travel partnership) application is already pending/i, '合作申请正在等待对方确认，请勿重复提交'],
  [/(?:distribution|travel partnership) relationship is already active/i, '双方已经建立合作关系'],
  [/(?:distribution|travel partnership) application is not pending/i, '该合作申请状态已变化，请刷新后重试'],
  [/supplier not found/i, '未找到该系统编号对应的景区供应商'],
  [/applicant tenant capability is unavailable/i, '申请方的旅行社业务能力当前不可用'],
  [/network error|failed to fetch|connection/i, '网络连接失败，请检查网络后重试'],
  [/timeout|deadline exceeded/i, '请求超时，请稍后重试'],
  [/invalid credentials|incorrect password|authentication failed/i, '账号或密码错误'],
  [/unauthorized|invalid token|token.*expired/i, '登录状态已失效，请重新登录'],
  [/forbidden|permission denied|access denied/i, '当前账号无权执行此操作'],
  [/not found|record not found/i, '未找到相关数据'],
  [/duplicate|already exists|unique constraint/i, '相同记录已经存在，请勿重复提交'],
  [/insufficient.*stock|out of stock/i, '库存不足'],
  [/insufficient.*balance/i, '账户余额不足'],
  [/insufficient.*quota|quota.*exceeded/i, '可用额度不足'],
  [/AI provider did not return a final answer|increase max_output_tokens/i, '模型没有返回最终计划；请将“最大输出 Token”设为自动（0），或确认当前模型支持返回最终 JSON'],
  [/AI provider returned an empty plan/i, 'AI 服务返回了空的最终计划，请检查模型名称、接口地址和最大输出 Token'],
  [/AI provider returned no choices/i, 'AI 服务返回格式不兼容，请检查接口地址或模型配置'],
  [/AI provider returned no supported tool calls|AI provider did not return a tool call|AI 未调用受支持的查询或预览工具/i, '模型没有调用受支持的查询或预览工具，请检查协议模式或模型是否支持工具调用'],
  [/authentication fails|invalid api key|invalid api-key|AI provider returned HTTP 401/i, 'AI API Key 无效或已过期，请检查密钥'],
  [/AI provider returned HTTP 404|model.*not found/i, 'AI 模型或接口地址不存在，请检查配置'],
  [/AI provider returned HTTP 429|rate limit/i, 'AI 服务请求过于频繁，请稍后重试'],
  [/not configured|adapter.*unavailable/i, '相关功能尚未配置'],
  [/unavailable|disabled|suspended/i, '当前业务暂不可用'],
  [/required|cannot be empty|missing/i, '请补充必填信息'],
  [/invalid|unsupported/i, '提交内容不符合要求'],
  [/expired/i, '相关数据已过期'],
  [/conflict/i, '数据状态已发生变化，请刷新后重试'],
]

export const localizeErrorMessage = (message: unknown, fallback = '操作失败，请稍后重试') => {
  const text = String(message || '').trim()
  if (!text) return fallback
  const noRefund = text.match(/^product\s+(.+)\s+does not allow refunds$/i)
  if (noRefund) return `票种“${noRefund[1]}”设置为不可退，无法退款`
  if (/[\u3400-\u9fff]/.test(text)) return text
  if (!/[A-Za-z]{2,}/.test(text)) return text
  const matched = englishErrorRules.find(([pattern]) => pattern.test(text))
  if (matched) return matched[1]
  console.error('未翻译的接口错误：', text)
  return fallback
}

export const localizeDisplayText = (message: unknown, fallback = '系统返回了未识别的信息，请查看服务日志') =>
  localizeErrorMessage(message, fallback)
