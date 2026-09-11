# 手机网页核销

## 目标

为不方便安装闸机的点位提供一个 HTTPS 手机网页。工作人员使用现有验票员账号登录，选择已授权的检票点和 `handheld` 移动终端，然后用手机相机、二维码图片或手动票码完成核销。

这条链路只记录入园事实，不产生闸机开闸任务，也不改变闸机设备的物理控制协议。

## 业务边界

- 服务端从 JWT、员工资源范围、检票点和设备归属推导租户与景区；浏览器不能提交任意租户或跨景区设备。
- 会话有效期为 8 小时，原始会话令牌只在 `sessionStorage` 中保存，服务端只保存 SHA-256 摘要。
- 会话必须绑定一个供应商租户、一个检票点和一个 `handheld` 设备；每次新建会话会撤销同一员工的旧移动会话。
- 后续核销只提交票码和一次性请求号，设备、检票点和景区均从会话读取。
- `DeviceVerification.open_status` 对手机核销写为 `not_required`，因此不会出现在闸机物理结果待办中。
- 票权规则、有效期、订单支付状态、渠道售后隔离、景区归属和重复次数继续由现有 `TicketService` 统一校验。

## 后台准备

1. 在设备管理中新增类型为 `handheld` 的设备并绑定检票点。
2. 在员工管理中给验票员配置该检票点和移动设备的资源范围。
3. 使用 HTTPS 打开管理端的 `/mobile` 路径。服务端仅对同源 `/mobile` 页面开放摄像头权限，API 和其他页面仍禁止摄像头；相机被拒绝时可以上传二维码图片或手动输入票码。

## 接口

- `GET /api/v1/mobile/targets`：返回当前员工可用的检票点和手持设备。
- `POST /api/v1/mobile/sessions`：创建短时移动会话，服务端校验设备类型、点位景区和员工资源范围。
- `POST /api/v1/mobile/session/heartbeat`：延长活跃设备心跳。
- `POST /api/v1/mobile/session/verify`：使用 `X-Mobile-Session` 会话令牌提交票码和请求号。
- `POST /api/v1/mobile/session/close`：主动关闭会话并将没有其他活跃会话的设备置为离线。

## 第一阶段验收

- Android Chrome 与 iOS Safari 在 HTTPS 下可打开摄像头并识别二维码。
- 相机拒绝、弱网、重复回调、重复点击、重复请求号、过期票、已核销票、跨点位票和未支付票均有明确结果。
- 同一请求号重试只返回原结果，不新增 `CheckInRecord`。
- 手机成功核销产生一条 `CheckInRecord` 和一条完成的 `DeviceVerification`，但不产生闸机 `pending` 任务。
- 员工越权、设备跨景区、租户冻结、会话过期和会话令牌泄露后的请求均 fail-closed。

第一阶段仍要求在线联网；离线手机票包、设备本地缓存和无网补传不在本次实现范围内。

## Web 与 Android 共用契约

手机网页和后续 Android 原生端共用以上接口和服务端状态，不在客户端复制票权、核销次数或退款判断。
原生端只替换登录存储、相机和网络适配层：令牌放入 Android Keystore/加密存储，扫码使用 CameraX/ML Kit，
核销请求继续通过 `X-Mobile-Session` 发送。

一次扫码在结果明确前必须保留同一个 `request_id` 和规范化后的票码。超时、断网或 `409` 处理中时只能
重试该请求号；只有收到明确的 `allow` 或 `deny` 后，下一张票才生成新的请求号。服务端摘要稳定绑定租户、
设备、点位、请求号和规范化票码，不绑定短时会话行，因此丢失响应后重新登录仍可安全重放同一请求。服务端返回的
`reason_code` 是跨端判断依据，`display_text` 只用于现场展示。常用值包括：

- `verified`：核验通过。
- `invalid_ticket`、`refunded`、`expired`、`not_started`：票券状态不允许入园。
- `already_used`、`benefit_exhausted`：当前点位或票组权益已用尽。
- `wrong_checkpoint`、`order_not_paid`：归属或订单状态不满足。
- `processing`：请求仍在处理，允许沿用原请求号重试。
- `manual_review`、`verification_failed`：需要按提示处理，不能在客户端自行放行。

网页端的“最近核销”只保存在当前页面，用于现场回看，不是服务端事实；Android 端可以提供同等的内存列表，
但不得把它当作离线票包或核销缓存。会话恢复时应立即调用心跳校验，失败则清除本地会话并回到点位选择。
网页端会在 `sessionStorage` 临时保存一笔结果未确认的 `request_id` 与规范化票码，刷新后仍只能重试这笔请求；
明确成功/拒绝或关闭会话后立即清除该记录。Android 端应把同一职责放入加密的 `SessionStore`，不要复制业务判断。

原生适配分层建议：`SessionStore`（加密会话）、`VerificationTransport`（以上 API 和幂等重试）、
`ScannerAdapter`（相机/相册/手动输入）和 `VerificationPresenter`（按 `reason_code` 显示状态）。
这四层之外不新增业务状态机，也不直接调用 `/hardware/*` 设备密钥接口。
