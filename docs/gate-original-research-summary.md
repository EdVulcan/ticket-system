# 闸机原厂程序与刷码通信研究汇总

更新时间：2026-08-24

## 1. 设备与原厂程序

> 原厂逆向的长期注释已迁入独立目录：[docs/gate-original/README.md](./gate-original/README.md)。本汇总只保留当前现场结论和下一步，不复制完整反汇编。

- 控制机：Linux，ARMv7，Freescale i.MX6；BusyBox 用户空间，无 Bash、systemd。
- 原厂主程序：`/usr/sendinfo/ticket -qws`。
- 原厂启动脚本：`/usr/sendinfo/start_a9.sh`；看门狗：`/usr/sendinfo/watchdog`。
- 原厂扫码配置：`/usr/sendinfo/ttyUSB.conf` 内容为 `/dev/ttyUSB2`。
- 原厂进程当前打开了 `/dev/ttyUSB0`、`/dev/ttyUSB2`、`/dev/ttyUSB3` 和 `/dev/ttymxc3`。
- `/dev/input/event0` 名称为 `SENDINFO_KEY`，本次联合监听没有捕获输入事件；扫码器不是 HID 键盘路径。

## 2. 原厂程序备份

已通过维护 SSH 只读打包并传回本地：

`artifacts/gate-original/gate-original-20260823.tar.gz`

备份包含原厂二进制、启动脚本、看门狗、串口配置、原厂启动器、系统启动配置和相关数据库。远端与本地 SHA-256 一致：

```text
CB8766B67382559CFF64319A2F0E43052820A3A359C1C5EF463615EE41191D9D
```

备份过程没有停止或修改原厂程序，也没有发送 CAN/开闸指令。备份目录目前仅存在于本地工作区，未推送 GitHub。

## 3. gate-client 在线问题

- `ticket-gate status=ok` 只代表本机 `127.0.0.1:19300` 服务可用，不代表云端心跳在线。
- 曾出现 `/dev/urandom` 权限错误，导致 Go 客户端 heartbeat goroutine 触发 fatal error 并退出；这解释了部分离线现象。
- 重启 `S98-ticket-gate` 后设备恢复在线，说明当时网络并未中断，主要问题是客户端进程/心跳恢复链路。
- 目前仍应补充“客户端崩溃或维护会话断开后自动恢复心跳”的测试与可观测性。

## 4. CAN 被动监听结果

`can0` 正常，`candump can0` 可用。监听全程只读，没有注入任何帧。

### 无扫码基线

10 秒无扫码监听：0 帧。

### 有效票

单次有效扫码产生 4 帧，固定交替结构：

```text
0x122 -> 0x123 -> 0x122 -> 0x123
```

示例：

```text
<0x122> [8] 1e 13 00 00 68 09 4c 90
<0x123> [6] 3b 43 b3 17 62 b1
<0x122> [8] 00 00 e1 ed c6 0e cc 3a
<0x123> [8] d9 52 dc 3e ac 46 45 0e
```

之前一次出现“两次感应”时共捕获 8 帧，实际是两个完整的 4 帧事务。`0x122` 首字节出现 `0x1b`、`0x1c`、`0x1d`、`0x1e` 的递增现象，可能是事务计数器；payload 没有明文票码特征，可能包含随机数、校验或加密数据。

### 无效票

- CAN：0 帧。
- `confirmbarticket` 没有新增记录。
- `querybarticket` 新增查询记录，但 `ticketstate` 为空。

这表明无效票在原厂票务校验阶段就被拦截，没有进入闸机控制事务。

## 5. 当前结论

当前已经确认：

1. 原厂扫码器走 `/dev/ttyUSB2`，原厂程序独占该串口。
2. 有效票通过校验后才会产生 4 帧 CAN 事务。
3. 无效票不会产生 CAN 帧。
4. “两次感应”对应两次独立事务，而不是一帧重复显示。
5. 目前不能安全地直接 `cat /dev/ttyUSB2`，否则可能与原厂程序竞争串口数据。

## 6. 后续建议

- 有效票和重复/多次放行样本已完成，不再重复现场测试；剩余采样只针对尚未覆盖的过期票、断网/超时和控制板异常，并且继续保持被动监听。
- 研究原厂数据库中的 `barcodebuf` 与 CAN 事务的对应关系，优先完成静态分析和协议建模。
- 如果必须获取串口原始字节，应使用硬件串口分线/监听，或准备经过验证的系统调用级跟踪工具；不要直接抢占 `/dev/ttyUSB2`。
- 在 `gate-client` 中实现 CAN 驱动前，必须保留原厂程序回退路径，并完成只读解析与仿真测试。

## 7. 采集文件

- `artifacts/gate-original/gate-original-20260823.tar.gz`
- `artifacts/gate-original/can-baseline-20260823.log`
- `artifacts/gate-original/can-scan-20260823.log`
- `artifacts/gate-original/can-scan-next-20260823.log`
- `artifacts/gate-original/joint-can-20260823.log`
- `artifacts/gate-original/joint-scan-correlation-20260823.md`
- `artifacts/gate-original/invalid-scan-20260823.md`

## 8. 本轮整合后的工程结论

本轮静态分析已经从“函数/地址列表”推进到可验证的业务链：

```text
有效扫码/票务确认
 -> event 0x1f68
 -> canSendPer_addEth
 -> C5/C7 业务候选载荷（03 01、数量、XOR）
 -> commKey 检查与加密
 -> 0x122/0x123 事务
 -> P07=6：CanDataDecrypt → UserEvent(0x1f6a) → Form::event → CanGateBack6
 -> P07=5：proceedCanMessage → 0x14/0x15/0x71/0x73/0x74 → canEvent/队列
```

这证明了“有效票确认后才启动受保护的控制板事务”，并且静态闭合了当前 P07=6 的上层事件处理：`UserEvent(0x1f6a)` 在 `Form::event(0x755c0)` 的跳转表中命中 case `0x76384`，直接调用 `Form::CanGateBack6(0x5db1c)`，payload 来自 `event+0x28`。结合标准 Qt4 `QObjectData` 布局（`parent` 位于 `d_ptr+8`）和构造时传入的 `Form*` 父对象，`CanBusThread+0x8` 也可静态归因为 `Form*`；现场只需核对具体 Qt4 版本。上述结果仍没有证明“哪一帧就是开闸脉冲”或“哪一个反馈代表杆已完成转动”。P07=5 的 `proceedCanMessage`/`canEvent` 仍是另一条接收实现，不能混入当前 P07=6。`0x142/0x152` 目前只能叫 Z/F 队列发送 ID；`CanGateBack5/6` 的数量、声音和 UI 状态也不能替代物理反馈。

运行时审计同时确认：

- `/dev/urandom` 权限曾导致 gate-client heartbeat fatal 退出；重启 S98 后恢复在线是有记录的恢复路径。
- S98 没有持续崩溃监督；原厂 `start_a9.sh` 的 `ps | grep "ticket"` 可能把 `ticket-gate` 算作原厂进程，存在原厂程序退出后不被拉起的共存风险。
- 原厂 watchdog、陈旧 `/tmp/heartbeat` 和固定时间窗口都可能引起整机重启，因此后台“离线”必须与现场进程、日志和心跳时间联合判断。

因此当前 gate-client 的安全边界是：继续做云端认证、心跳、核销幂等、状态恢复和诊断；真实驱动保持未配置，不能写 CAN、不能抢占 `/dev/ttyUSB2`，也不能把 `allowed=true` 直接当作 `opened`。

详细报告：

- `artifacts/gate-original/protocol-payload-analysis-20260823.md`
- `artifacts/gate-original/runtime-evidence-audit-20260823.md`
- `artifacts/gate-original/gate-client-boundary-review-20260823.md`
