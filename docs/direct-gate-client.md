# 闸机直连客户端部署说明

## 1. 工作方式

`gate-client` 运行在闸机控制机上，不需要 Redis、本地数据库或额外网关服务。扫码流程如下：

1. 扫码器或闸机厂商程序调用本机 `POST /scan`。
2. 客户端使用设备独立密钥对请求方法、路径、时间戳、nonce、请求号和正文签名。
3. 云端校验租户、供应能力、设备状态、景区和检票点，持久化请求并执行票权核销。
4. 只有云端返回 `allow` 时，客户端才调用真实开闸驱动。
5. 客户端把明确的 `opened` 或 `failed` 回报云端；无法确认物理结果时保持 `pending` 并锁定本地记录，要求现场恢复。核销事实和物理开闸结果分别保留，便于定位“票有效但闸机未动作”的现场故障。

客户端会把“正在验票、准备开闸、开闸结果未知、已开闸待回报和开闸失败待处理”持久化到本机状态文件。网络响应丢失时复用原请求号，不重复核销；已经物理开闸但回报失败时只补回报，不再次开闸。驱动调用超时且无法判断闸门是否动作时保持失败关闭，必须由现场确认实际结果。

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

### 2.1 一键安装（推荐）

发布包中的 `gate-client/` 目录包含 Linux amd64 的 `gate-client`、`gate-provision`、systemd 单元和安装脚本；嵌入式 ARMv7/BusyBox 控制机使用旁边独立的 `gate-client-armv7/` 目录。对应安装脚本会创建受限的 `ticket-gate` 用户、状态目录和配置目录，随后只提示输入管理端生成的一次性绑定码：

```bash
sudo bash ./install.sh --server-url https://tickets.example.com --package-dir .
```

绑定码只通过 HTTPS JSON body 发送，安装器不会接受命令行参数或环境变量形式的绑定码，也不会把它写入日志。服务端从租约决定租户、景区、设备和序列号，安装器只能解密服务端返回的配置包。安装器会原子写入 `/etc/ticket-gate/gate-client.env`、生成本地扫码令牌、用新设备密钥确认完成，并启用 `ticket-gate.service`。网络中断时保留临时公私钥和配置，重新执行并输入同一绑定码即可恢复；确认完成后临时私钥自动删除。

### 2.1.1 ARMv7/BusyBox 控制机

部分现场闸机使用 i.MX6 ARMv7 和 BusyBox `init`，没有 `bash` 或 `systemd`。发布脚本会同时生成独立的 `gate-client-armv7/` 目录；该目录不能与 amd64 包混用。控制机应满足 `uname -m` 返回 `armv7l`，并提供 `adduser`、`start-stop-daemon` 和可写的 `/etc`、`/var`。

在控制机上执行：

```sh
cd /path/to/gate-client-armv7
sh ./install-busybox.sh --server-url https://tickets.example.com --package-dir .
```

安装器会把二进制放到 `/usr/local/lib/ticket-gate`，配置写入 `/etc/ticket-gate/gate-client.env`，创建受限 `ticket-gate` 用户，并安装 `/etc/init.d/S98-ticket-gate`。BusyBox 启动脚本只在网络初始化脚本之后启动客户端，不改写厂商已有的 `S99-fluidlauncher`；对于明确使用本地 RTC 的厂商脚本，会在启动前按 `UTC=no`/`--localtime` 从 RTC 校准系统时间，避免心跳使用未来的 UTC 时间戳；同时在降权前修正重启后可能恢复为 `root:root 0660` 的 `/dev/urandom` 权限，客户端仍以受限 `ticket-gate` 用户运行。日志写入 `/var/lib/ticket-gate/gate-client.log`。现场检查使用 `ticket-gate status|doctor|logs`。如果设备没有 `adduser` 或 `start-stop-daemon`，安装器会拒绝以 root 长期运行客户端，需先按厂商系统提供受限服务账号或启动方式。

### 2.2 完整现场流程

1. **准备控制机**：amd64/systemd 控制机使用 `gate-client/`；i.MX6 ARMv7/BusyBox 控制机使用 `gate-client-armv7/`，分别确认对应架构、启动方式和可写目录，且控制机能访问云端 HTTPS；把对应目录通过 U 盘或受控 SCP 复制到控制机。当前发布包不包含真实三辊闸驱动，拿到控制板协议后还需单独安装对应适配器。
2. **后台登记设备**：租户管理员进入“设备管理”，新增类型为“闸机”的设备，选择正确景区和（如已知）检票点，填入现场设备序列号，状态保持离线。新增设备时后台会自动生成设备密钥，不需要人工抄写密钥到闸机。
3. **生成一次性绑定码**：在该设备的“生成安装绑定”中填写安装原因，生成有效期约 10 分钟的绑定码。绑定码只在页面显示一次；确认没有旧客户端在线后再复制。设备在线或故障（任何非明确 `offline` 状态）、租户被冻结、景区票务能力关闭或服务端没有 HTTPS 公网地址时，系统会拒绝生成/领取。
4. **执行安装器**：在控制机执行下面的命令，只有服务器地址出现在命令行，绑定码会在交互提示中输入：

   ```bash
   cd /path/to/gate-client
   sudo bash ./install.sh --server-url https://tickets.example.com --package-dir .
   ```

   安装器会创建受限系统用户、申请加密安装配置、写入权限为 `0640` 的 `/etc/ticket-gate/gate-client.env`、启用并启动 systemd 服务。租户、景区、设备和序列号都由服务器租约决定，现场不能在安装命令中改写。
5. **确认安装**：安装器显示“安装绑定完成”后执行 `sudo ticket-gate doctor` 和 `sudo ticket-gate status`。如果网络在最后确认阶段中断，安装器会提示“配置已安全写入但确认暂未完成”并退出；重新执行同一条安装命令，输入同一个绑定码即可继续，脚本会识别 `/var/lib/ticket-gate/.provisioning.json` 并恢复，不会重复生成设备密钥。
6. **接入真实驱动**：在拿到三辊闸控制板的串口/网络协议、开闸脉冲和反馈定义后，配置独立驱动适配器，再重启服务并按正常票、重复票、退款票、网络中断和超时场景验收。`GATE_DRIVER_URL` 为空时只能做心跳/诊断，物理开闸结果固定为 `unknown`；不要用正式票做“试扫”，避免票权已核销但现场未放行。
7. **轮换或撤销**：更换控制机或绑定错误时，先在后台撤销未完成绑定，再停止旧客户端；不要手工复制数据库中的密钥。已完成安装需要轮换设备密钥时，可在发布包目录执行 `sudo bash ./install.sh --server-url https://tickets.example.com --package-dir . --rebind`，脚本会停止 systemd 客户端、把旧配置移到 root-only 备份后重新绑定；现场手工启动的旧进程仍需先自行停止。

现场自检命令：

```bash
ticket-gate doctor
ticket-gate status
ticket-gate logs
```

安装脚本不会创建或选择任何真实闸机驱动。`GATE_DRIVER_URL` 为空时客户端仍可发送心跳并提供诊断，但物理开闸结果始终为 `unknown`，绝不会使用 mock 返回 `opened`；此时不要扫描正式游客票，因为云端核销可能已经消耗票权而闸机不会动作。

Linux 控制机建议以独立受限账号运行，并由 `systemd` 保持进程存活。状态文件目录只授予该账号读写权限；扫码入口仅监听本机回环地址。真实驱动适配器接入前，使用明确返回 `opened`/`failed`/`unknown` 的测试驱动完成恢复验收。

## 3. 配置

| 环境变量 | 必填 | 说明 |
|---|---|---|
| `GATE_SERVER_URL` | 是 | 云端地址，生产环境必须为 HTTPS |
| `GATE_SYSTEM_CODE` | 是 | 景区商户的系统编号 |
| `GATE_SERIAL_NUMBER` | 是 | 后台登记的设备序列号 |
| `GATE_DEVICE_KEY` | 是 | 后台创建设备或轮换密钥时仅显示一次的原始密钥；使用一键安装时由 `gate-provision` 自动写入，不需手工填写 |
| `GATE_MAINTENANCE_SECRET` | 否 | 后台“远程维护”生成的独立维护凭据；为空则不建立维护通道 |
| `GATE_MAINTENANCE_URL` | 启用维护时建议显式配置 | 后台返回的 `wss://.../api/v1/hardware/maintenance/ws` 地址；留空时从 `GATE_SERVER_URL` 的主机推导；不把令牌写进 URL |
| `GATE_DRIVER_URL` | 生产必填 | 本机硬件适配器地址；拿到真实厂商协议前可指向测试驱动 |
| `GATE_SCAN_TOKEN` | 是 | 本机扫码接口令牌，应使用高强度随机值；使用一键安装时自动生成 |
| `GATE_STATE_FILE` | 生产必填 | 待处理开闸状态文件，例如 `/var/lib/ticket-gate/state.json`，目录仅允许闸机程序账号访问 |
| `GATE_SCAN_LISTEN` | 否 | 默认 `127.0.0.1:19300`，不应直接暴露公网 |
| `GATE_ALLOW_INSECURE_HTTP` | 仅本地调试 | 设为 `true` 才允许使用 HTTP 云端地址 |

### 3.1 管理端生成安装绑定

租户管理员在设备管理中为明确离线闸机填写安装原因并生成约 10 分钟的一次性绑定码。在线或故障设备不能抢占绑定；旧客户端应先停止并确认状态回到 `offline`。绑定码过期、撤销、租户冻结、景区能力关闭或设备被禁用时服务端零凭据写入。服务端只保存绑定码哈希，claim 使用服务端 `SELECT FOR UPDATE`，同一 `installation_id` 和 X25519 公钥的网络重试返回同一加密包，其他安装器拒绝。安装确认通过新设备 HMAC 完成，确认后服务端清除加密包。

安装接口为 `POST /api/v1/hardware/provision`，确认接口为设备签名保护的 `POST /api/v1/hardware/provision/confirm`；两者均不把长期密钥放入 URL、命令行、二维码或日志。数据库 schema 107 增加 `DeviceProvisioningLease` 及租户/景区/设备/操作者 ownership guard。

原始设备密钥不写入安装器的 claim 请求正文，也不应写入日志、安装包或源码；它只在 HTTPS 返回的 X25519 加密配置包中传递。云端数据库只保存加密后的设备密钥。走安装绑定时，claim 会自动轮换该设备密钥；无法使用安装绑定的旧设备才需要先在后台手工轮换一次。任何密钥泄露都应立即轮换。

### 3.2 服务器承载的临时 SSH 维护通道

远程维护是一个独立的、默认关闭的运维能力，不参与票权核销、开闸命令或设备 HMAC。租户管理员在设备管理中先生成/轮换维护凭据，再按原因创建短时会话；凭据和会话令牌都只返回一次。Linux `gate-client` 通过 HTTPS 服务器建立 WSS 长连接，服务端只允许把管理端字节流转发到闸机控制机本机的固定 `127.0.0.1:22`，不接受服务端下发目标地址，也不提供通用 TCP、SOCKS、VPN 或横向内网代理。

启用前必须同时满足：

1. 反向代理为维护路径提供 HTTPS/WSS 和部署侧的证书/客户端准入策略；应用配置 `maintenance.enabled: true` 时 `server.public_base_url` 必须是 `https://`。
2. 闸机控制机的维护凭据只保存在 gate-client 账号可读的受限配置中，不能与 `GATE_DEVICE_KEY` 混用。
3. 管理员创建会话后使用页面显示的地址和令牌运行 `gate-ssh`（建议作为 OpenSSH `ProxyCommand`），会话关闭、过期、服务重启或凭据轮换都会立即断开。

构建并使用代理命令：

```bash
go build -o gate-ssh ./cmd/gate-ssh
ssh -o "ProxyCommand=gate-ssh --url '<websocket_url>' --token '<session_token>'" root@127.0.0.1
```

`root@127.0.0.1` 只是 SSH 客户端显示的本机目标名；真正的 TCP 连接始终由闸机上的 gate-client 固定拨号到 `127.0.0.1:22`。不要把会话令牌放入脚本仓库、工单、浏览器历史或日志。当前实现不宣称已经具备厂商三辊闸控制板协议；SSH 只用于现场诊断、配置和协议嗅探准备，仍需人工审批和现场操作规范。

## 4. 本机扫码接口

受保护的 `GET /status` 可供现场诊断查看设备序列号、待恢复数量、各恢复阶段和适配器是否已配置；它不会返回设备原始密钥、票码或云端凭据。

```http
POST /scan
Authorization: Bearer <GATE_SCAN_TOKEN>
Content-Type: application/json

{"ticket_code":"票码","media_type":"qr_code"}
```

响应会明确区分：

- `allowed=false`：云端拒绝核销，不调用开闸驱动。
- `allowed=true, opened=true`：核销成功且驱动确认开闸。
- `allowed=true, opened=false`：票权核销成功，但开闸驱动明确失败或结果未知。结果未知时不会自动二次开闸，现场人员按下述恢复流程处理。

`physical_status` 会明确返回 `pending`、`opened`、`failed` 或 `unknown`，现场程序不得只根据 `allowed=true` 就放行。

拒绝结果同时返回 `voice_code` 和可选的 `voice_file`，由本地驱动播放预置音频，不需要临时从云端下载。

### 开闸结果未知的恢复

现场确认闸门实际状态后调用本机接口：

```http
POST /recovery
Authorization: Bearer <GATE_SCAN_TOKEN>
Content-Type: application/json

{"ticket_code":"票码","media_type":"qr_code","action":"confirm_opened"}
```

- `confirm_opened`：确认闸门已经打开。再次扫描原票码时只补交开闸结果，不再次开闸。
- `confirm_not_opened`：确认闸门没有打开。再次扫描原票码时重新调用开闸驱动，不重复核销票权。
- `retry_open`：控制板明确拒绝或已知失败，现场确认没有通行后重新调用开闸驱动，不重复核销票权。

## 5. 开闸驱动协议

当前提供协议无关的驱动 seam，并带有标准 HTTP 测试适配器。客户端向 `GATE_DRIVER_URL` 发送：

```json
{
  "request_id": "本次核销请求号",
  "open_duration_ms": 5000,
  "display_text": "欢迎光临",
  "voice_code": "child_ticket"
}
```

适配器必须明确返回物理结果：

```json
{"status":"opened","vendor_code":"OK"}
```

`status` 只能是 `opened`、`failed` 或 `unknown`。空正文的 2xx 不能视为已经开闸；网络超时、5xx 或无法解析结果默认按 `unknown` 处理，不能自动再次发出开闸命令。明确的 4xx 拒绝可以返回 `failed`。`voice_code` 是供应商按票种维护并在售票时固化的本地音频编号，例如 `welcome`、`child_ticket`。服务器不生成语音，也不在核销时传输音频；厂商驱动必须把编号映射到闸机电脑或控制器中预先安装的音频文件、设备音轨。取得具体闸机品牌的 SDK 或串口/TCP 协议后，只需实现这一驱动 seam，不改云端核销模型。

系统拒绝提示使用固定编号：无效票为 `invalid`，未生效为 `not_started`，已过期为 `expired`，核销次数已满为 `already_used`。这些编号对应的音频必须在现场部署闸机程序时预先安装。

当前基础架构仍是在线核销；断网离线授权需要单独的签名票包、有效期、冲突补传和风险规则，尚未启用。

## 6. 现场验收

正式启用前必须用真实设备验证：正常票、重复扫码、已退款票、过期票、跨景区票、驱动超时、闸机断电、网络中断、服务重启和连续峰值核销。A 景区票在 B 景区必须始终拒绝。
