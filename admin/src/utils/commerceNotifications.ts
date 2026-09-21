export type CommerceNotification = {
  id: string | number
  businessType: string
  locationId?: string | number | null
  locationName?: string | null
  orderId?: string | number | null
  orderNo?: string | null
  eventType: string
  title: string
  message: string
  createdAt: string
  readAt?: string | null
  status: string
}

const firstValue = (...values: unknown[]) => values.find(value => value !== undefined && value !== null && value !== '')

export const normalizeNotification = (raw: any): CommerceNotification | null => {
  const id = firstValue(raw?.id, raw?.notification_id, raw?.notificationId)
  if (id === undefined) return null

  const businessType = String(firstValue(raw?.business_type, raw?.businessType, raw?.business, '') || '').trim()
  const status = String(firstValue(raw?.status, raw?.read_at || raw?.readAt ? 'read' : 'unread', 'unread') || 'unread').toLowerCase()
  const readAt = firstValue(raw?.read_at, raw?.readAt, null) as string | null
  const createdAt = String(firstValue(raw?.created_at, raw?.createdAt, raw?.timestamp, '') || '')
  const eventType = String(firstValue(raw?.event_type, raw?.eventType, raw?.type, '') || '')
  const orderId = firstValue(raw?.order_id, raw?.orderId, raw?.order?.id, null) as string | number | null
  const rawOrderNo = firstValue(raw?.order_no, raw?.orderNo, raw?.order?.order_no, raw?.order?.orderNo, null)
  const orderNo = rawOrderNo === null || rawOrderNo === undefined ? null : String(rawOrderNo).trim() || null
  const rawLocationName = firstValue(raw?.location_name, raw?.locationName, raw?.location?.name, null)
  const locationName = rawLocationName === null || rawLocationName === undefined ? null : String(rawLocationName).trim() || null

  return {
    id: id as string | number,
    businessType,
    locationId: firstValue(raw?.location_id, raw?.locationId, null) as string | number | null,
    locationName,
    orderId,
    orderNo,
    eventType,
    title: String(firstValue(raw?.title, raw?.order?.title, '新订单') || '新订单'),
    message: String(firstValue(raw?.message, raw?.body, raw?.order?.summary, '') || ''),
    createdAt,
    readAt,
    status,
  }
}

export const normalizeNotificationList = (payload: any): CommerceNotification[] => {
  const values = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.data)
      ? payload.data
      : Array.isArray(payload?.items)
        ? payload.items
        : Array.isArray(payload?.notifications)
          ? payload.notifications
          : Array.isArray(payload?.data?.items)
            ? payload.data.items
            : []
  return values.map((value: any) => normalizeNotification(value)).filter((value: CommerceNotification | null): value is CommerceNotification => Boolean(value))
}

export const normalizeUnreadCount = (payload: any): number => {
  const raw = firstValue(payload?.unread_count, payload?.unreadCount, payload?.count, payload?.data?.unread_count, payload?.data?.unreadCount, payload?.data?.count, payload)
  const count = Number(raw)
  return Number.isFinite(count) && count > 0 ? Math.floor(count) : 0
}

export const isUnread = (item: CommerceNotification) => !item.readAt && !['read', '已读', 'closed'].includes(item.status)

export const isOrderNotification = (item: CommerceNotification) => item.eventType === 'order.paid' || item.eventType.startsWith('order.')

export const notificationTime = (value: string) => {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value || '刚刚'
  const diff = Math.max(0, Date.now() - timestamp)
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  return new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(timestamp)
}
