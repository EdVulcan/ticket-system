import { onMounted, onUnmounted, watch } from 'vue'

const REFRESH_INTERVAL_MS = 2_500
const MAX_REFRESH_ATTEMPTS = 48

export function usePendingRefundRefresh(hasPendingRefund: () => boolean, refresh: () => Promise<void>, isRefreshing = () => false) {
  let timer: ReturnType<typeof window.setTimeout> | undefined
  let attempts = 0
  let inFlight = false
  let disposed = false

  const isVisible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
  const clearScheduledRefresh = () => {
    if (timer !== undefined) window.clearTimeout(timer)
    timer = undefined
  }
  const stop = () => {
    clearScheduledRefresh()
  }
  const schedule = () => {
    if (disposed || timer !== undefined || inFlight || isRefreshing() || !isVisible() || !hasPendingRefund() || attempts >= MAX_REFRESH_ATTEMPTS) return
    timer = window.setTimeout(() => {
      timer = undefined
      void run()
    }, REFRESH_INTERVAL_MS)
  }
  const run = async () => {
    if (disposed || inFlight || isRefreshing() || !isVisible() || !hasPendingRefund() || attempts >= MAX_REFRESH_ATTEMPTS) return
    attempts += 1
    inFlight = true
    try {
      await refresh()
    } catch {
      // Silent polling never changes a refund state or repeatedly interrupts the operator.
    } finally {
      inFlight = false
      if (!disposed) sync()
    }
  }
  const sync = () => {
    if (!hasPendingRefund()) {
      attempts = 0
      stop()
      return
    }
    schedule()
  }
  const onVisibilityChange = () => {
    if (!isVisible()) {
      stop()
      return
    }
    attempts = 0
    sync()
  }

  watch(() => [hasPendingRefund(), isRefreshing()], sync, { immediate: true })
  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibilityChange)
    sync()
  })
  onUnmounted(() => {
    disposed = true
    document.removeEventListener('visibilitychange', onVisibilityChange)
    stop()
  })
}
