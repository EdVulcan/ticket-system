# 闸机直连客户端部署说明

## 1. 工作方式

`gate-client` 运行在闸机控制机上，不需要 Redis、本地数据库或额外网关服务。扫码流程如下：

1. 扫码器或闸机厂商程序调用本机 `POST /scan`。
2. 客户端使用设备独立密钥对请求方法、路径、时间戳、nonce、请求号和正文签名。
3. 云端校验租户、供应能力、设备状态、景区和检票点，持久化请求并执行票权核销。
4. 只有云端返回 `allow` 时，客户端才调用真实开闸驱动。
5. 客户端把 `opened` 或 `failed` 回报云端。核销事实和物理开闸结果分别保留，便于定位“票有效但闸机未动作”的现场故障。

重复发送相同扫描请求号会重放首次核销结果，不会再次消耗核销次数。nonce 只能使用一次，超过五分钟的签名会被拒绝。设备接口默认限制为每台设备每分钟 600 次请求。

## 2. 构建

在 `backend` 目录执行：

```powershell
go build -o gate-client.exe ./cmd/gate-client
```

Linux 控制机可执行：

```bash
go build -o gate-client ./cmd/gate-client
```

客户端仅使用 Go 标准库，不需要 CGO。

## 3. 配置

| 环境变量 | 必填 | 说明 |
|---|---|---|
| `GATE_SERVER_URL` | 是 | 云端地址，生产环境必须为 HTTPS |
| `GATE_SYSTEM_CODE` | 是 | 景区商户的系统编号 |
| `GATE_SERIAL_NUMBER` | 是 | 后台登记的设备序列号 |
| `GATE_DEVICE_KEY` | 是 | 后台创建设备或轮换密钥时仅显示一次的原始密钥 |
| `GATE_DRIVER_URL` | 生产必填 | 厂商开闸 HTTP 驱动地址 |
| `GATE_SCAN_TOKEN` | 是 | 本机扫码接口令牌，应使用高强度随机值 |
| `GATE_SCAN_LISTEN` | 否 | 默认 `127.0.0.1:19300`，不应直接暴露公网 |
| `GATE_ALLOW_INSECURE_HTTP` | 仅本地调试 | 设为 `true` 才允许使用 HTTP 云端地址 |

密钥不写入请求正文，也不应写入日志、安装包或源码。设备密钥泄露后应在后台立即轮换。

## 4. 本机扫码接口

```http
POST /scan
Authorization: Bearer <GATE_SCAN_TOKEN>
Content-Type: application/json

{"ticket_code":"票码","media_type":"qr_code"}
```

响应会明确区分：

- `allowed=false`：云端拒绝核销，不调用开闸驱动。
- `allowed=true, opened=true`：核销成功且驱动确认开闸。
- `allowed=true, opened=false`：票权核销成功，但开闸驱动未配置或执行失败；云端会记录开闸失败，现场人员按授权流程处理。

## 5. 开闸驱动协议

当前提供标准 HTTP 驱动边界。客户端向 `GATE_DRIVER_URL` 发送：

```json
{
  "request_id": "本次核销请求号",
  "open_duration_ms": 5000,
  "display_text": "欢迎光临",
  "voice_code": "child_ticket"
}
```

`voice_code` 是供应商按票种维护并在售票时固化的本地音频编号，例如 `welcome`、`child_ticket`。服务器不生成语音，也不在核销时传输音频；厂商驱动必须把编号映射到闸机电脑或控制器中预先安装的音频文件、设备音轨。`voice_file` 仅为旧驱动的固定拒绝提示兼容字段，新驱动应使用 `voice_code`。驱动返回 HTTP 2xx 才视为物理开闸成功。没有配置驱动、找不到本地音频、连接失败或返回非 2xx 时均保持失败，不会模拟成功。取得具体闸机品牌的 SDK 或串口/TCP 协议后，只需实现这一驱动边界，不改云端核销模型。

系统拒绝提示使用固定编号：无效票为 `invalid`，未生效为 `not_started`，已过期为 `expired`，核销次数已满为 `already_used`。这些编号对应的音频必须在现场部署闸机程序时预先安装。

## 6. 现场验收

正式启用前必须用真实设备验证：正常票、重复扫码、已退款票、过期票、跨景区票、驱动超时、闸机断电、网络中断、服务重启和连续峰值核销。A 景区票在 B 景区必须始终拒绝。
