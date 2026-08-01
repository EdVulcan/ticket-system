export interface HardwareResult {
  success: boolean
  message: string
}

type HardwareMethod = (payload?: unknown) => Promise<HardwareResult>

function unavailable(message: string): HardwareResult {
  return { success: false, message }
}

export async function printTicket(payload: unknown): Promise<HardwareResult> {
  try {
    const wailsPrint = window.go?.main?.App?.PrintTicket as HardwareMethod | undefined
    if (wailsPrint) return await wailsPrint(payload)
    if (window.api?.printTicket) return await window.api.printTicket(payload)
    return unavailable('printer bridge is unavailable')
  } catch (error) {
    return unavailable(error instanceof Error ? error.message : 'printer bridge failed')
  }
}

export async function readCard(): Promise<HardwareResult> {
  try {
    const wailsRead = window.go?.main?.App?.ReadCard as HardwareMethod | undefined
    if (wailsRead) return await wailsRead()
    if (window.api?.readCard) return await window.api.readCard()
    return unavailable('identity card reader bridge is unavailable')
  } catch (error) {
    return unavailable(error instanceof Error ? error.message : 'identity card reader bridge failed')
  }
}
