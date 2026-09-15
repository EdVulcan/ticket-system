# Android 移动核销端

原生 Kotlin + Jetpack Compose 应用，包名 `top.edvulcan.ticket.verify`，最低支持 Android 7（API 24）。

## 当前能力

- 员工账号登录并读取租户名称。
- 按服务端员工资源范围选择检票点和手持设备。
- CameraX + ML Kit 二维码识别。
- 扫码只预检，确认后才核销。
- 整单共享码选择本次核销人数，默认 1；一票一码固定为 1。
- 近期重复码必须显式确认继续核销。
- 未决操作使用原 `operation_id` 查询和恢复，结果不明时锁定下一张票。
- 登录令牌、移动会话和未决操作使用 Android Keystore AES-GCM 加密保存。
- 成功/拒绝声音与震动反馈，最近五笔结果仅保存在当前进程。

票权、点位、数量、退款锁和幂等始终由服务端决定；客户端不缓存离线票包，也不能自行扩大核销范围。

## 构建

Windows 终端使用 Android Studio 自带 JDK：

```powershell
$env:JAVA_HOME='D:\Android\Android Studio\jbr'
.\gradlew.bat testDebugUnitTest assembleDebug --console=plain
```

Debug APK：`app/build/outputs/apk/debug/app-debug.apk`。

服务端地址由 `app/build.gradle.kts` 的 `BuildConfig.API_BASE_URL` 提供。生产发布前应改为环境化构建配置，并建立独立签名、版本升级和受控分发流程。

## 真机验收

至少覆盖 Android 7、9、11、12，并验证：相机授权/拒绝后恢复、弱网、锁屏切回、进程被杀后的未决操作恢复、共享码批量核销、近期重复保护、退款票、错误点位和连续扫码。

当前不支持离线核销。没有真机或模拟器时，构建成功不能替代相机及厂商 ROM 验收。
