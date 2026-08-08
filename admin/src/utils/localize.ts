const englishErrorRules: Array<[RegExp, string]> = [
  [/tenant activation requires approved, unexpired qualification and contract|tenant qualification or contract is not active/i, '启用前请先确认资质已通过；已填写的资质和合同到期日必须晚于当前时间'],
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
