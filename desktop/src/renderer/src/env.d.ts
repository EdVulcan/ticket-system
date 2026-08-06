/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

interface HardwareBridgeResult {
  success: boolean
  message: string
}

interface Window {
  api?: {
    printTicket?: (payload: unknown) => Promise<HardwareBridgeResult>
    readCard?: () => Promise<HardwareBridgeResult>
  }
  go?: {
    main?: {
      App?: {
        PrintTicket?: (payload: unknown) => Promise<HardwareBridgeResult>
        ReadCard?: () => Promise<HardwareBridgeResult>
        CheckForUpdate?: () => Promise<{
          available: boolean
          current_version: string
          version: string
          message: string
        }>
        InstallUpdate?: () => Promise<HardwareBridgeResult>
      }
    }
  }
}
