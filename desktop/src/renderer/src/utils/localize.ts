const englishErrorRules: Array<[RegExp, string]> = [
  [/network error|failed to fetch|connection/i, '网络连接失败，请检查网络后重试'],
  [/timeout|deadline exceeded/i, '请求超时，请稍后重试'],
  [/invalid credentials|incorrect password|authentication failed/i, '工号或密码错误'],
  [/unauthorized|invalid token|token.*expired/i, '登录状态已失效，请重新登录'],
  [/forbidden|permission denied|access denied/i, '当前工号无权执行此操作'],
  [/not found|record not found/i, '未找到相关数据'],
  [/duplicate|already exists|unique constraint/i, '相同记录已经存在，请勿重复提交'],
  [/insufficient.*stock|out of stock/i, '库存不足'],
  [/not configured|adapter.*unavailable/i, '相关设备或支付方式尚未配置'],
  [/unavailable|disabled|suspended/i, '当前业务暂不可用'],
  [/required|cannot be empty|missing/i, '请补充必填信息'],
  [/invalid|unsupported/i, '提交内容不符合要求'],
]

export const localizeErrorMessage = (message: unknown, fallback = '操作失败，请稍后重试') => {
  const text = String(message || '').trim()
  if (!text) return fallback
  if (!/[A-Za-z]{2,}/.test(text)) return text
  const matched = englishErrorRules.find(([pattern]) => pattern.test(text))
  if (matched) return matched[1]
  console.error('未翻译的接口错误：', text)
  return fallback
}
