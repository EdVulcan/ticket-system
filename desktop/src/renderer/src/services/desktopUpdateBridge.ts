export interface DesktopUpdateInfo {
  available: boolean
  current_version: string
  version: string
  message: string
}

export interface DesktopUpdateResult {
  success: boolean
  message: string
}

export async function checkDesktopUpdate(): Promise<DesktopUpdateInfo | null> {
  const check = window.go?.main?.App?.CheckForUpdate
  if (!check) return null
  try {
    return await check()
  } catch {
    return null
  }
}

export async function installDesktopUpdate(): Promise<DesktopUpdateResult> {
  const install = window.go?.main?.App?.InstallUpdate
  if (!install) return { success: false, message: '当前环境不支持自动更新' }
  try {
    return await install()
  } catch (error) {
    return { success: false, message: error instanceof Error ? error.message : '安装更新失败' }
  }
}
