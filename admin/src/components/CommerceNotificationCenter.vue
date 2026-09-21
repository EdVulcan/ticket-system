<template>
  <el-popover
    v-model:visible="popoverVisible"
    placement="bottom-end"
    :width="380"
    trigger="click"
    :teleported="true"
    popper-class="commerce-notification-popper"
  >
    <template #reference>
      <button class="notification-trigger" type="button" aria-label="新订单通知" title="新订单通知">
        <el-icon><Bell /></el-icon>
        <span v-if="unreadCount > 0" class="notification-badge">{{ unreadCount > 99 ? '99+' : unreadCount }}</span>
      </button>
    </template>

    <div class="notification-panel">
      <div class="notification-panel-header">
        <div>
          <strong>新订单通知</strong>
          <span v-if="unreadCount > 0">{{ unreadCount }} 条未读</span>
          <span v-else>暂无未读通知</span>
        </div>
        <div class="notification-panel-actions">
          <button
            class="notification-icon-button"
            type="button"
            :aria-label="soundEnabled ? '关闭提示音' : '开启提示音'"
            :title="soundEnabled ? '关闭提示音' : '开启提示音'"
            @click="toggleSound"
          >
            <el-icon><Bell v-if="soundEnabled" /><Mute v-else /></el-icon>
          </button>
          <button class="notification-icon-button" type="button" aria-label="刷新通知" title="刷新通知" :disabled="loading" @click="refresh">
            <el-icon :class="{ 'is-loading': loading }"><Refresh /></el-icon>
          </button>
        </div>
      </div>

      <div v-if="loading && notifications.length === 0" class="notification-empty">正在加载通知…</div>
      <div v-else-if="notifications.length === 0" class="notification-empty">暂无新订单</div>
      <div v-else class="notification-list">
        <button
          v-for="item in notifications"
          :key="String(item.id)"
          class="notification-item"
          :class="{ 'is-unread': isItemUnread(item) }"
          type="button"
          @click="openNotification(item)"
        >
          <span class="notification-dot" aria-hidden="true" />
          <span class="notification-item-copy">
            <span class="notification-item-title">
              <strong>{{ item.title }}</strong>
              <small>{{ businessLabel(item.businessType) }}</small>
            </span>
            <span class="notification-item-message">{{ item.message || orderLabel(item) }}</span>
            <span class="notification-item-meta">{{ orderLabel(item) }} · {{ locationLabel(item) }} · {{ notificationTime(item.createdAt) }}</span>
          </span>
        </button>
      </div>
      <p class="notification-note">点击通知只标记为已读，不会自动接单。提示音需手动开启。</p>
    </div>
  </el-popover>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Bell, Mute, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'
import {
  CommerceNotification,
  isOrderNotification,
  isUnread,
  normalizeNotificationList,
  normalizeUnreadCount,
  notificationTime,
} from '@/utils/commerceNotifications'

const props = withDefaults(defineProps<{
  enabled: boolean
  businessTypes?: string[]
}>(), {
  businessTypes: () => [],
})

const router = useRouter()
const popoverVisible = ref(false)
const notifications = ref<CommerceNotification[]>([])
const unreadCount = ref(0)
const loading = ref(false)
const initialized = ref(false)
const latestId = ref<string | number | undefined>(undefined)
// Keep audio opt-in for every page session. Creating/resuming an AudioContext
// from a timer can be blocked by browser autoplay policies, so it is only
// primed from the explicit sound-toggle click below.
const soundEnabled = ref(false)
let audioContext: AudioContext | null = null
const pollTimer = ref<number | undefined>(undefined)
const marking = new Set<string>()

const silentConfig = { skipErrorToast: true } as any

const validBusinessType = (value: unknown): value is 'restaurant' | 'retail' => value === 'restaurant' || value === 'retail'
const availableBusinessTypes = computed(() => props.businessTypes.filter(validBusinessType))
const notificationContextKey = computed(() => `${props.enabled}:${availableBusinessTypes.value.join(',')}`)

const isItemUnread = (item: CommerceNotification) => isUnread(item)
const businessLabel = (value: string) => ({ restaurant: '餐饮', retail: '电商' } as Record<string, string>)[value] || '商业订单'
const orderLabel = (item: CommerceNotification) => item.orderNo ? `订单 ${item.orderNo}` : '订单信息待同步'
const locationLabel = (item: CommerceNotification) => {
  if (item.locationName) return item.locationName
  if (item.locationId !== null && item.locationId !== undefined && String(item.locationId).trim() !== '') return `履约地点 #${item.locationId}`
  return '履约地点待同步'
}

const playChime = () => {
  if (!soundEnabled.value || typeof window === 'undefined') return
  try {
    const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext
    if (!AudioContextClass) {
      soundEnabled.value = false
      return
    }
    const context = audioContext
    if (!context || context.state !== 'running') return
    const oscillator = context.createOscillator()
    const gain = context.createGain()
    oscillator.type = 'sine'
    oscillator.frequency.setValueAtTime(880, context.currentTime)
    oscillator.frequency.exponentialRampToValueAtTime(660, context.currentTime + 0.12)
    gain.gain.setValueAtTime(0.0001, context.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.06, context.currentTime + 0.01)
    gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 0.18)
    oscillator.connect(gain)
    gain.connect(context.destination)
    oscillator.start()
    oscillator.stop(context.currentTime + 0.2)
  } catch {
    // Browsers can reject audio until a user gesture; notifications still work silently.
  }
}

const mergeNotifications = (incoming: CommerceNotification[], initial: boolean) => {
  const existingKeys = new Set(notifications.value.map(item => String(item.id)))
  const hasNewUnreadOrder = !initial && initialized.value && incoming.some(item => !existingKeys.has(String(item.id)) && isUnread(item) && isOrderNotification(item))
  const merged = initial ? incoming : [...incoming, ...notifications.value]
  const unique = Array.from(new Map(merged.map(item => [String(item.id), item])).values())
  unique.sort((left, right) => Date.parse(right.createdAt || '') - Date.parse(left.createdAt || ''))
  notifications.value = unique.slice(0, 20)
  const numericIDs = incoming.map(item => Number(item.id)).filter(value => Number.isFinite(value))
  if (numericIDs.length > 0) latestId.value = Math.max(...numericIDs)
  if (hasNewUnreadOrder) playChime()
  initialized.value = true
}

const loadNotifications = async (initial = false) => {
  try {
    const params: Record<string, string | number> = { unread_only: 'true', page_size: 20 }
    if (!initial && latestId.value !== undefined) params.after_id = latestId.value
    const response = await request.get('/commerce/notifications', { params, ...silentConfig })
    mergeNotifications(normalizeNotificationList(response.data), initial)
  } catch {
    // Notification availability must not interrupt ordinary order work.
  }
}

const loadUnreadCount = async () => {
  try {
    const response = await request.get('/commerce/notifications/unread-count', silentConfig)
    unreadCount.value = normalizeUnreadCount(response.data)
  } catch {
    // Keep the last known badge when the notification service is unavailable.
  }
}

const refresh = async () => {
  if (!props.enabled || loading.value || document.visibilityState !== 'visible') return
  loading.value = true
  try {
    await Promise.all([loadNotifications(!initialized.value), loadUnreadCount()])
  } finally {
    loading.value = false
  }
}

const stopPolling = () => {
  if (pollTimer.value !== undefined) window.clearInterval(pollTimer.value)
  pollTimer.value = undefined
}

const startPolling = () => {
  stopPolling()
  if (!props.enabled || document.visibilityState !== 'visible') return
  pollTimer.value = window.setInterval(() => { void refresh() }, 5_000)
}

watch(notificationContextKey, (value, previous) => {
  if (value === previous) return
  notifications.value = []
  unreadCount.value = 0
  initialized.value = false
  latestId.value = undefined
  if (props.enabled) {
    void refresh()
    startPolling()
  } else {
    stopPolling()
  }
})

const handleVisibility = () => {
  if (document.visibilityState === 'visible') {
    void refresh()
    startPolling()
  } else {
    stopPolling()
  }
}

const toggleSound = () => {
  soundEnabled.value = !soundEnabled.value
  if (!soundEnabled.value) {
    if (audioContext) void audioContext.close().catch(() => undefined)
    audioContext = null
    return
  }

  // This handler runs from a user gesture, which is the point at which
  // browsers allow an AudioContext to be resumed for future poll results.
  try {
    const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext
    if (!AudioContextClass) {
      soundEnabled.value = false
      return
    }
    audioContext = audioContext || new AudioContextClass()
    void audioContext.resume()
  } catch {
    soundEnabled.value = false
    audioContext = null
  }
}

const openNotification = async (item: CommerceNotification) => {
  const key = String(item.id)
  if (marking.has(key)) return
  marking.add(key)
  try {
    if (isUnread(item)) {
      await request.post(`/commerce/notifications/${encodeURIComponent(key)}/read`, undefined, silentConfig)
      item.readAt = new Date().toISOString()
      item.status = 'read'
      unreadCount.value = Math.max(0, unreadCount.value - 1)
    }
  } catch {
    // Navigation remains useful even when the best-effort read marker is delayed.
  } finally {
    marking.delete(key)
  }
  popoverVisible.value = false
  if (item.businessType === 'restaurant' || item.businessType === 'retail') {
    const query: Record<string, string> = { tab: 'orders' }
    if (item.orderNo) query.order = item.orderNo
    if (item.locationId !== null && item.locationId !== undefined && String(item.locationId).trim() !== '') query.location_id = String(item.locationId)
    await router.push({ path: `/commerce/${item.businessType}`, query })
  }
}

onMounted(() => {
  if (!props.enabled) return
  window.addEventListener('focus', handleVisibility)
  window.addEventListener('online', handleVisibility)
  document.addEventListener('visibilitychange', handleVisibility)
  void refresh()
  startPolling()
})

onBeforeUnmount(() => {
  stopPolling()
  if (audioContext) void audioContext.close().catch(() => undefined)
  audioContext = null
  window.removeEventListener('focus', handleVisibility)
  window.removeEventListener('online', handleVisibility)
  document.removeEventListener('visibilitychange', handleVisibility)
})
</script>

<style scoped>
.notification-trigger { position: relative; display: inline-flex; align-items: center; justify-content: center; width: 34px; height: 34px; padding: 0; color: var(--ui-text-secondary); background: transparent; border: 1px solid transparent; border-radius: var(--ui-radius); cursor: pointer; }
.notification-trigger:hover, .notification-trigger:focus-visible { color: var(--ui-primary); background: var(--ui-primary-soft); border-color: var(--ui-border); outline: none; }
.notification-trigger .el-icon { font-size: 19px; }
.notification-badge { position: absolute; top: -4px; right: -6px; min-width: 17px; height: 17px; padding: 0 4px; color: #fff; background: #d94b4b; border: 2px solid #fff; border-radius: 999px; font-size: 10px; font-weight: 700; line-height: 13px; text-align: center; }
.notification-panel { width: 100%; min-width: 0; }
.notification-panel-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding-bottom: 12px; border-bottom: 1px solid #edf0f4; }
.notification-panel-header > div:first-child { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.notification-panel-header strong { color: #202b3c; font-size: 14px; }
.notification-panel-header span { color: #87909e; font-size: 11px; }
.notification-panel-actions { display: flex; align-items: center; gap: 2px; }
.notification-icon-button { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; color: #778191; background: transparent; border: 0; border-radius: 4px; cursor: pointer; }
.notification-icon-button:hover, .notification-icon-button:focus-visible { color: var(--ui-primary); background: #f3f6f9; outline: none; }
.notification-icon-button:disabled { cursor: default; opacity: 0.6; }
.notification-list { max-height: 360px; margin: 0 -8px; overflow-y: auto; }
.notification-item { display: flex; width: 100%; gap: 9px; padding: 11px 8px; color: inherit; text-align: left; background: transparent; border: 0; border-bottom: 1px solid #f0f2f5; cursor: pointer; }
.notification-item:hover, .notification-item:focus-visible { background: #f6f8fb; outline: none; }
.notification-item:last-child { border-bottom: 0; }
.notification-dot { flex: 0 0 auto; width: 7px; height: 7px; margin-top: 5px; background: transparent; border-radius: 50%; }
.notification-item.is-unread .notification-dot { background: var(--ui-primary); box-shadow: 0 0 0 3px var(--ui-primary-soft); }
.notification-item-copy { min-width: 0; display: flex; flex: 1; flex-direction: column; gap: 3px; }
.notification-item-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.notification-item-title strong { overflow: hidden; color: #273243; font-size: 13px; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.notification-item-title small { flex: 0 0 auto; color: #667085; font-size: 10px; }
.notification-item-message, .notification-item-meta { overflow: hidden; color: #647084; font-size: 12px; line-height: 18px; text-overflow: ellipsis; white-space: nowrap; }
.notification-item-meta { color: #99a1ae; font-size: 11px; }
.notification-empty { padding: 32px 8px; color: #87909e; font-size: 12px; text-align: center; }
.notification-note { margin: 9px 0 0; padding-top: 9px; color: #9ba3af; border-top: 1px solid #edf0f4; font-size: 11px; line-height: 16px; }
:global(.commerce-notification-popper) { padding: 14px 16px 12px !important; }
:global(.commerce-notification-popper .el-popper__arrow::before) { background: #fff !important; }
@media (max-width: 640px) {
  :global(.commerce-notification-popper) { width: min(380px, calc(100vw - 24px)) !important; max-width: calc(100vw - 24px); }
}
</style>
