# 原厂闸机程序静态分析

更新时间：2026-08-24
分析对象：`artifacts/gate-original/gate-original-20260823.tar.gz`
备份 SHA-256：`CB8766B67382559CFF64319A2F0E43052820A3A359C1C5EF463615EE41191D9D`

本文只记录本地备份的静态分析和已经完成的现场被动观察。分析期间没有启动、停止、替换或修改闸机上的原厂程序，没有读取扫码串口原始字节，也没有向 CAN 总线发送帧或开闸指令。

## 1. 结论先行

1. 原厂主程序是 `/usr/sendinfo/ticket -qws`，运行在 ARMv7/i.MX6、BusyBox init 环境；原厂扫码器配置为 `/dev/ttyUSB2`，代码路径是 Qt 串口 `readAll()`，不是 HID 键盘路径。
2. 现场配置 `P07_GateComType=6` 对应 SocketCAN 接收路径。程序初始化 `can0`，设置 500000 bit/s，然后使用 raw CAN socket；单帧最大 8 字节。
3. 静态代码和现场被动抓包相互印证：有效票才进入四帧事务 `0x122 → 0x123 → 0x122 → 0x123`；无效票没有 CAN 帧。这说明原厂先完成票务校验，再尝试与闸机控制板通信。
4. `0x121`、`0x122`、`0x123` 是通信/业务事务层；`0x142`、`0x152` 是两个发送队列使用的 CAN ID；`0x488` 是配置/控制参数路径。仅凭目前证据，尚不能把其中任何一个 ID 直接宣称为三辊闸“开闸脉冲”。
5. 原厂 `S99-fluidlauncher` 在头部无条件 `exit 0`，因此其后面的启动代码不可达。它更像被禁用的 Qt demo 启动器，不能作为“原厂桌面开机启动成功”的依据；现场主界面实际由 `/usr/sendinfo/ticket -qws` 提供。
6. 原厂程序和 `gate-client` 不能同时抢占同一个扫码串口；CAN 总线虽然可以被动监听，但两个程序同时写 CAN 会产生竞态。当前 `gate-client` 没有真实驱动配置时，只应作为心跳/诊断客户端运行。

## 2. 分析边界与证据等级

### A. 反汇编/符号直接确认

- ELF 为 ARM 32-bit `ET_EXEC`，保留 C++ 符号和部分调试信息；主程序使用 Qt4、SQLite、OpenSSL/mbedTLS、SocketCAN。
- `CanBusThread`、`SwingGate`、`BarCode`、`Form::CanGateBack5/6` 等函数名和调用关系来自符号表及 ARM 反汇编。
- `CanBusThread::run` 读取 P07 通信类型；类型 6 走 `recvfrom` 的 SocketCAN 路径。
- `CanBusThread::SetType`、`SetPer` 的 `CanWrite` 调用前 CAN ID 字面量是 `0x488`。
- `canSendCmdQueueZ` 的 CAN ID 字面量是 `0x142`，`canSendCmdQueueF` 的 CAN ID 字面量是 `0x152`。
- `CanBusThread::run` 的接收分支比较 `0x121`、`0x122`、`0x123`。
- `Form::CanGateBack6` 直接比较响应首字节 `0xc6`、`0xc8`、`0xcc`、`0xe2`、`0xe4`。

### 2.1 本轮新增的高价值闭合

独立协议报告已把“地址清单”压缩为可验证的字段和状态链：

```text
票务确认
 -> SwingGateSetper
 -> event 0x1f68
 -> canSendPer_addEth
 -> C5/C7 + 数量 + XOR 校验
 -> commKey 检查与 CanDataEncrypt
 -> 0x122/0x123 事务
 -> P07=6：CanDataDecrypt → UserEvent(0x1f6a) → Form::event → CanGateBack6
 -> P07=5：proceedCanMessage → canEvent/队列
```

其中 `canSendPer_addEth` 的未加密业务候选明确为：

```text
flag != 0: C5 03 01 count_hi count_lo xor
flag == 0: C7 03 01 count_hi count_lo xor
```

最后一个字节是前五字节逐字节 XOR，不是随机数。`0x121` 是 commKey 请求/握手路径，返回数据写入 commKey 区域；`0x142/0x152` 仍只能命名为 Z/F 队列发送 ID。上述闭合仍不足以证明任何一帧就是三辊闸物理开闸。

P07=6 receiver 的 Qt4 ABI 对照和字段推导见
`artifacts/gate-original/qt4-abi-receiver-note-20260824.md`；该结论只用于确认事件
投递对象为 `Form*`，不替代控制板 ACK 或物理杆反馈证据。

### B. 现场被动观察支持的推断

- 一次有效扫码产生四帧固定顺序；一次“两次感应”产生两组四帧，而不是一帧重复。
- 0x122 的首字节出现连续递增值，符合事务序号/计数器的可能形态，但还不能仅凭样本确定其溢出规则或方向。
- 无效票不产生 CAN 帧，说明票务校验在 CAN 控制之前完成。

### C. 尚未闭合

- 0x121 返回字段的厂商业务语义和密钥轮换/失效生命周期。
- 0x123 解密后的业务字段与三辊闸控制板动作的对应关系。
- 0x142/0x152 队列分别对应哪一类方向、显示、计数或开闸动作。
- `setPoleUpDown` 的参数值和 4 字节业务载荷的物理含义。
- 0xc6/0xc8/0xcc/0xe2/0xe4 各响应码的厂商语义。
- P93 条码波特率枚举值 `1` 对应的实际波特率。

P07=6 的上层事件映射不再属于“尚未闭合”：`UserEvent(0x1f6a)` 的跳转表项
`0x76384` 已直接调用 `Form::CanGateBack6(0x5db1c)`；标准 Qt4 `QObjectData`
布局和构造父对象参数进一步把 `CanBusThread+0x8` 归因到 `Form*`。剩余的是目标
Qt4 版本的只读核对，以及更关键的控制板 ACK、杆反馈和动作语义；这些仍不能凭
事件处理函数名称推断。

## 3. 设备与原厂启动结构

### 3.1 运行环境

- 控制机：Freescale i.MX6，ARMv7，BusyBox 用户空间。
- 没有 `bash` 和 `systemd`；启动由 BusyBox `init`、`/etc/init.d/rcS` 及 SysV 风格脚本完成。
- 主程序：`/usr/sendinfo/ticket -qws`。
- 看门狗：`/usr/sendinfo/watchdog 5 2 0`，由 `start_a9.sh` 后台拉起。
- 原厂 Web 服务：脚本在 `/sbin/boa` 存在且未运行时启动它。
- 现场原厂程序打开过多个设备节点；扫码配置明确指向 `/dev/ttyUSB2`。

### 3.2 `S99-fluidlauncher` 的实际状态

`extracted/etc/init.d/S99-fluidlauncher` 在环境变量和 `case "$1"` 逻辑之前就执行了无条件 `exit 0`。因此后面的 `fluidlauncher -qws` 循环在该文件当前版本中不可达。

文件中确实保留了 Qt demo 启动逻辑（`/usr/bin/launcher/fluidlauncher`、`QWS_DISPLAY=LinuxFb:/dev/fb0`），但不能据此认为它会在开机时运行。后续如果要保留原厂桌面，必须先研究真正的启动入口和原厂程序退出/恢复行为；不要直接把这个脚本当成已生效的桌面自启动脚本。

### 3.3 `start_a9.sh` 的共存风险

脚本通过下面的文本匹配判断原厂进程是否存在：

```sh
process_num=`ps | grep "ticket" |grep -v "grep" |wc -l`
```

新程序路径包含 `ticket-gate`，可能被误计入 `ticket` 进程数量。这样一来，原厂程序崩溃后，脚本有机会错误地跳过 `/usr/sendinfo/ticket -qws` 的拉起。未来若需要共存，必须把守护逻辑改成精确 PID/可执行文件判断，并在隔离环境验证；本次没有改动现场脚本。

脚本还会在以下情况下直接 `reboot`：

- 检测到 `/web/config/usrconfig-updata.db` 并完成替换；
- 到达固定的每日重启时间窗口；
- `/tmp/heartbeat` 超过按 P2a 配置计算的最大间隔。

这意味着原厂自身就具有主动重启行为，分析新客户端的“离线”时必须区分云端心跳故障、客户端崩溃和原厂整机重启。

运行时审计还确认：S98 在降权前修正 `/dev/urandom`，但没有持续监督 gate-client 崩溃；原厂 watchdog 和陈旧 `/tmp/heartbeat` 都可能触发整机复位。完整的只读采集顺序和证据缺口见 `artifacts/gate-original/runtime-evidence-audit-20260823.md`。

## 4. 扫码输入路径

### 4.1 配置和代码

`extracted/usr/sendinfo/ttyUSB.conf` 内容为：

```text
/dev/ttyUSB2
```

符号 `BarCode::ReadMyCom` 调用 Qt 串口对象的 `readAll()`；`BarCode::run` 还包含针对厂商帧的 `0xaa`、`0x55` 等特征判断。现场联合监听 `/dev/input/event0`（名称 `SENDINFO_KEY`）没有捕获扫码输入，因此当前样本不支持“扫码器是 HID 键盘”的假设。

### 4.2 共存结论

串口设备通常只有一个消费方能可靠取得完整字节流。原厂程序已经打开 `/dev/ttyUSB2` 时，不能再让 `gate-client` 直接 `cat` 或 `open` 该设备；这样会造成字节竞争、丢帧或改变原厂验票行为。安全方案只有：

1. 继续由原厂程序读取扫码器，采用其可审计的本机接口/事件转发结果；或
2. 在停用原厂程序并完成回退方案后，由新驱动独占扫码器；或
3. 使用硬件级串口分线/监听获取原始样本，而不抢占原厂文件描述符。

## 5. CAN 初始化与帧层

### 5.1 接口初始化

`CanBusThread` 的构造/运行路径出现以下命令：

```text
canconfig can0 stop
canconfig can0 bitrate 500000
canconfig can0 start
```

P07 现场值为 `6`，与 `recvfrom` 的 SocketCAN 分支一致。`CanWrite` 通过 `ioctl` 获取接口索引、`bind(AF_CAN)`，然后调用 `sendto`；载荷长度大于 8 会拒绝或进入错误分支。

### 5.2 CAN ID 映射

| CAN ID | 代码证据 | 当前可确认含义 | 置信度 |
|---|---|---|---|
| `0x121` | `CanBusThread::getCommKeyTimerOut(0xcc414)`/`run` 分支 | CAN 通信密钥请求或握手路径 | 静态确认路径，业务语义待厂商资料 |
| `0x122` | `CanDataEncrypt`、`run` 分支 | 通信序号/随机数据交换 | 静态确认 + 被动样本 |
| `0x123` | `CanDataEncrypt/Decrypt`、`run` 分支 | 业务数据承载 | 静态确认 + 被动样本 |
| `0x142` | `canSendCmdQueueZ` 字面量 | Z 队列的发送帧 | 静态确认，物理语义未知 |
| `0x152` | `canSendCmdQueueF` 字面量 | F 队列的发送帧 | 静态确认，物理语义未知 |
| `0x488` | `SetType`、`SetPer` 调用 `CanWrite` | 闸机类型/参数配置或控制路径 | 静态确认，物理动作未知 |
| `0x499` | `run` 接收前置分支 | 另一类配置/诊断分支 | 仅见分支，语义未闭合 |
| `0x445` | `run` 接收前置分支 | 另一类配置/诊断分支 | 仅见分支，语义未闭合 |

`0x121/0x122/0x123` 是接收路径中明确比较的 ID；`0x142/0x152/0x488` 是发送函数中的字面量。不要把“被程序发送/接收”直接等同于“控制电机”，必须再找出具体 payload、控制板应答和现场动作的闭环。

### 5.3 业务 payload 字段

`canSendPer_addEth` 在加密前构造 6 字节候选载荷：首字节按 flag 选择 `C5`/`C7`，随后为固定 `03 01`、大端数量和 XOR 校验。只有 `this+0x3c` 表示 commKey 可用时才继续发送；否则记录错误并退出。这是“有效票触发业务请求”的直接证据，不是可直接重放的 CAN 帧。

## 6. 两层协议保护

### 6.1 通用 DES 封装层：`Encode`/`Decode`

`CanProt_Send`/`CanProt_Rece` 调用 `mbedtls_des_setkey_enc/dec` 与 `mbedtls_des_crypt_ecb`。反汇编可直接确认：

- `Encode` 输入长度上限为 `0x1d`；追加随机字节和校验后按 8 字节对齐；
- 校验是对前 `length-1` 字节逐字节累加并取 8 bit；
- 模式参数选择不同密钥表/密钥调度；
- `Decode` 要求输入长度为 8 的倍数，先 DES ECB 解密，再验证末尾累加校验。

这是通用协议封装层，不应与 0x123 业务 payload 的 MD5/XOR 机制混为一谈。

### 6.2 业务事务层：`CanDataEncrypt`/`CanDataDecrypt`

静态代码确认的流程为：

1. 自增 `sendTimes`，通过 `long2Byte` 形成发送序号 `commNo_Send`。
2. 生成 `yyyyMMddhhmmsszzz` 时间串，哈希后取前 4 字节作为 `randomData_Send`。
3. 将 `commNo_Send + randomData_Send` 放入 0x122 事务。
4. 构造字符串：

   ```text
   commKey+commNo_Send+randomData_Send+SENDINFO
   ```

5. 对该字符串做 MD5，按字节 XOR 业务 payload，再通过 0x123 业务路径发送。

接收方向的 `CanDataDecrypt` 对应使用 `commKey+CommNo_Recv+randomData_Recv+SENDINFO`，先恢复 payload。当前 P07=6 分支随后构造业务事件类型 `0x1f6a` 的 `UserEvent`（`0x5c` 只是对象分配大小），将结果投递到 `CanBusThread+0x8`；`proceedCanMessage` 的直接调用点只在 P07=5/AppCanRece 分支，不能把两条路径混写。

## 7. 现场被动样本与代码关联

### 7.1 有效票

一次有效扫码的被动 CAN 样本：

```text
0x122 [8] 1d 13 00 00 45 3f c8 2f
0x123 [6] 21 ca 20 fe 41 68
0x122 [8] 00 00 e2 ed 88 7a c0 16
0x123 [8] 39 7c 83 fc 0c 6c ad e7
```

另一次样本首字节为 `0x1b`、`0x1c`，随后同样各自出现 0x123、0x122、0x123。样本中的 payload 没有可直接读取的票码明文，符合业务层保护存在的预期。

### 7.2 无效票

无效票测试没有新增 `confirmbarticket`，仅出现查询记录，CAN 被动监听为 0 帧。这与 `run` 只在上层票务流程确认后进入 CAN 事务的调用链一致。

### 7.3 对“开闸”的谨慎结论

当前样本只能证明“有效票触发了一个四帧通信事务”。没有同时具备：

- 控制板方向/发送方标记；
- 0x123 解密后的字段；
- 控制板反馈与闸杆转动/显示计数的同步记录；
- 单独的配置动作和动作结果对照。

所以还不能从四帧样本反推出可以安全复刻的开闸帧，更不能在正式闸机上注入猜测帧。

## 8. 控制与反馈相关函数

### 8.1 `SetType`、`SetPer`

`CanBusThread::SetType`（地址 `0xca7b0`）和 `SetPer`（地址 `0xcac54`）都构造带长度/校验的 payload，并以 `0x488` 调用 `CanWrite`。它们明显是闸机类型、参数或权限配置路径，不是已经证实的单次开闸路径。

### 8.2 `setPoleUpDown`

`setPoleUpDown`（地址 `0xce038`）构造 4 字节中间 payload，把调用参数写入第 2 个字节，再经过对象的发送序号/加密准备；队列空闲时会触发 Z 队列（`0x142`）发送。函数名表明它与“杆上下”有关，但参数值、方向定义和控制板反馈仍未从静态代码闭合，不能直接拿来做现场开闸测试。

### 8.3 接收反馈

`Form::CanGateBack6`（地址 `0x5db1c`）按响应首字节处理 `0xc6`、`0xc8`、`0xcc`、`0xe2`、`0xe4`，其中两条路径把后续两个字节组合为 16 位数并更新界面/状态。新增的 `Form::event(0x755c0)` 跳转表证据已确认 P07=6 的 `UserEvent(0x1f6a)` 进入该函数，payload 来自 `event+0x28`。`Form::CanGateBack5`（地址 `0x5e230`）处理事件类型 `0x14`、`0x73`、`0x74`，包含通行计数、闸机状态和 UI 更新路径，并在计数差异时调用 `CanGateCheckPassNum`。这些函数有助于继续命名响应字段，但不足以证明哪一个码代表“杆已转动完成”。

### 8.4 `proceedCanMessage`

`CanBusThread::proceedCanMessage`（地址 `0xcf2c4`）属于 P07=5/AppCanRece 分支，在该分支按事件类型 `0x71`、`0x14`、`0x15` 触发 `canEvent`，并在不同队列状态下调用 `canSendCmdQueueZ/F`。当前 P07=6 的 `0x123` 分支没有直接调用它，而是投递业务类型 `0x1f6a` 的 `UserEvent`；该事件已由 `Form::event` 静态映射到 `CanGateBack6`，所以不能再把 P07=5 的 `proceedCanMessage` 当作 P07=6 的直接处理器。

事件 `0x14` 至少读取 payload[0..2]，payload[2] 非零时更新通行数量/UI并持久化；`0x73/0x74` 会处理前部字段及数量状态。`CanGateBack6` 对 `C6/C8/CC/E2/E4` 的条件和 byte2/3 大端数值已在协议报告列出，但这些都是业务计数、声音或状态路径，不能命名为“杆已转动”。

## 9. 与新 `gate-client` 的边界

当前新客户端的职责仍应保持为：云端设备认证、票权核销请求幂等、心跳、状态持久化和驱动 seam。真实三辊闸驱动尚未实现时，`driver_configured=false` 必须继续保持；不能因为看到了有效票的 CAN 帧就把 HTTP 测试适配器或猜测帧当成真实开闸。

推荐的最终接入边界：

1. 原厂程序继续运行时，新客户端不打开 `/dev/ttyUSB2`，只接收明确的本机验票结果或在隔离环境使用仿真输入。
2. 新驱动接管前，先在本地实现 CAN 帧解析器和协议 fixture，覆盖有效、重复、过期、无效和控制板异常，不连接现场 CAN 写入。
3. 取得厂商协议/备用控制板后，在隔离台架上验证“发送帧 → 控制板反馈 → 实际杆动作”，再考虑现场切换。
4. 任何物理开闸结果都必须独立记录为 `opened/failed/unknown`；不能以云端 `allowed=true` 代替物理确认。

## 10. 后续只读工作清单

1. 从 `CanGateBack6`、`proceedCanMessage` 和 `SetPer/setPoleUpDown` 的调用链继续标注 payload 字段与事件码，但不发送帧；P07=6 的事件到 `CanGateBack6` 映射已经完成。
2. 有效票和重复/多次放行样本已完成，不再重复；若继续现场采样，只补过期票、断网/超时和控制板异常，并同步记录数据库确认状态、控制板屏幕计数和杆动作时间；不要直接读取 `/dev/ttyUSB2`。
3. 确认 `P93_BarcodeBaud=1` 的实际波特率和扫码器帧结束符，优先从厂商配置/串口设置页取得，不通过抢占串口猜测。
4. 将已确认样本转成项目内的离线 parser fixture，先做解码和状态机测试，再评估驱动实现。
5. 在任何共存部署前修复并验证原厂 `start_a9.sh` 的进程识别误判风险；该修复应先在复制出的启动脚本/仿真系统验证，不直接改现场文件。

## 11. 相关文件

本文件是面向产品主线的当前结论；原厂逆向的长期注释和历史证据请从独立研究库入口读取：`docs/gate-original/README.md`。

- 原厂备份：`artifacts/gate-original/gate-original-20260823.tar.gz`
- 解包目录：`artifacts/gate-original/extracted/`
- 现场研究汇总：`docs/gate-original-research-summary.md`
- 有效/无效票被动样本：`artifacts/gate-original/can-scan-20260823.log`、`can-scan-next-20260823.log`、`joint-can-20260823.log`、`invalid-scan-20260823.md`
- 新客户端边界：`docs/direct-gate-client.md`
- 协议字段与反馈报告：`artifacts/gate-original/protocol-payload-analysis-20260823.md`
- 运行时/看门狗/共存审计：`artifacts/gate-original/runtime-evidence-audit-20260823.md`
- gate-client 边界复核：`artifacts/gate-original/gate-client-boundary-review-20260823.md`
