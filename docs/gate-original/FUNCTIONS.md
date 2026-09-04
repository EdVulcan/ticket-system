# 原厂函数注释索引

对象：ARMv7/i.MX6 原厂 `/usr/sendinfo/ticket -qws`。地址均为备份 ELF 中的静态地址，不是可直接在现场执行的命令。证据等级遵循 [README](./README.md)。

## 业务入口和发送链

| 符号 | 地址 | 已确认职责 | 未确认语义 | 证据 |
|---|---:|---|---|---|
| `Form::SwingGateSetper` | `0x3434c` | P07=6 时把数量（事件偏移 `+0x4e`）和 flag（`+0x1f`）放入事件 `0x1f68` | 数量字段的业务来源和 flag 的厂商命名 | A/B |
| `CanBusThread::canSendPer_addEth` | `0xcdbd4` | 构造 6 字节 C5/C7、03、01、数量、XOR；commKey 可用才继续发送 | C5 与 C7 的厂商业务名称 | A |
| `CanBusThread::canSendPer_replaceEth` | `0xcd798` | 构造 6 字节 C5/C7、03、00、数量、XOR；commKey 可用才继续发送 | `replace` 的厂商业务名称和与 add 的业务差异 | A |
| `CanBusThread::getCommKeyTimerOut` | `0xcc414` | CAN 路径置请求中标志，构造 8 字节空 payload，发送 `0x121`，等待 commKey 返回 | 密钥轮换、失效和权限语义 | A/B |
| `SwingGate::getCommKeyTimerOut` | `0xb4dbc` | 串行闸机路径的 commKey/握手准备，调用 `SwingGate::serialSendBuf` | 与 CAN 0x121 的对应关系、串行协议字段 | A/B |
| `CanBusThread::CanDataEncrypt` | `0xcc534` | 递增事务号；生成时间摘要；发送 `0x122`；为 `0x123` 计算 MD5/XOR | 0x122/0x123 在厂商协议中的正式名称 | A |
| `CanBusThread::CanDataDecrypt` | `0xccf10` | 用接收序号、摘要和 commKey 逆转业务保护后交给事件分派 | 错误分支与异常帧恢复 | A/B |
| `CanBusThread::proceedCanMessage` | `0xcf2c4` | P07=5/AppCanRece 分支分派 `0x14/0x15/0x71/0x73/0x74`；协调 Z/F 队列 | 事件码的厂商业务名称；不能直接归属于 P07=6 | A |
| `CanBusThread::run` | `0xcfc18` | P07=6 调 `recvfrom` 并按 `0x121/0x122/0x123` 分支；`0x123` 调 `CanDataDecrypt` 后构造业务类型 `0x1f6a` 的 `UserEvent`（`0x5c` 是分配大小）并投递到 `this+0x8`；P07=5 另调 `proceedCanMessage` | 在标准 Qt4 ABI 下 `this+0x8` 是构造时传入的 `Form*` 父对象；物理反馈语义未闭合 | A/B |
| `Form::event` | `0x755c0` | 自定义事件类型 `0x1f6a` 命中跳转表 case `0x76384`，从 `event+0x28` 取 payload 并直接调用 `Form::CanGateBack6(0x5db1c)` | 目标 Qt4 版本仍可只读核对；`CanGateBack6` 不等于控制板 ACK 或杆动作 | A |

`CanBusThread::event(0xce7bc)` 的分发表以 `0x1f5e` 为基准：`0x1f68`（索引 10）读取数量 `+0x4e` 和 flag `+0x1f` 后调用 `canSendPer_addEth(0xcdbd4)`；`0x1f69`（索引 11）读取同一字段后调用 `canSendPer_replaceEth(0xcd798)`。该映射来自跳转表和直接 `bl` 目标，不是函数名推断。

## 队列和杆相关路径

| 符号 | 地址 | 已确认职责 | 禁止的推断 |
|---|---:|---|---|
| `CanBusThread::canSendCmdQueueZ` | `0xcba64` | Z 队列通过 CAN ID `0x142` 发送 | 不能称为“开闸帧” |
| `CanBusThread::canSendCmdQueueF` | `0xcbbf0` | F 队列通过 CAN ID `0x152` 发送 | 不能称为“关闸/反向帧” |
| `CanBusThread::CanWriteByLib` | `0xcb4ac` | 将队列 payload 封装成底层 CAN 发送请求 | 底层成功不等于控制板 ACK 或杆动作 |
| `CanBusThread::setPoleUpDown` | `0xce038` | 4 字节中间 payload，把参数写入 byte1 后进入 Z 队列 | 参数 0/1 的方向、脉冲时长和物理结果 |

`canSendCmdQueueZ/F` 都先检查各自队列是否非空，再取队首 QByteArray 调用 `CanWriteByLib(0xcb4ac)`；Z 的字面量 ID 为 `0x142`，F 的字面量 ID 为 `0x152`。直接调用 `proceedCanMessage` 的代码位于 P07=5/AppCanRece 接收分支；这组队列语义不能自动迁移到当前 P07=6。发送结果会影响队列删除和约 1000 ms 定时器，但这些函数自身没有读取杆位置传感器，因此仍不能把发送成功命名为物理动作完成。
| `CanBusThread::SetType` | `0xca7b0` | 构造配置/参数 payload，通过 `0x488` 发送 | 不是已证实的单次开闸路径 |
| `CanBusThread::SetPer` | `0xcac54` | 构造配置/参数 payload，通过 `0x488` 发送 | 不是已证实的单次开闸路径 |

## 串行 `SwingGate` 路径（与 CAN 分开）

这组函数属于另一条厂商串行闸机实现。它们不能用来给 `CanBusThread` 的 SocketCAN ID 命名，也不能把串行 payload 直接当成 CAN payload。

| 符号 | 地址 | 已确认职责 |
|---|---:|---|
| `SwingGate::getCommKeyTimerOut` | `0xb4dbc` | 串行 commKey/握手准备，调用 `serialSendBuf` |
| `SwingGate::serialDataEncrypt` | `0xb59a8` | 串行业务数据保护 |
| `SwingGate::serialDataDecrypt` | `0xb513c` | 串行业务数据恢复 |
| `SwingGate::serialSendBuf` | `0xb6bd8` | 串行发送缓冲路径 |
| `SwingGate::serialSendCmdQueue` | `0xb6eac` | 串行命令队列 |
| `SwingGate::serialSendPer_addEth` | `0xb75f8` | 串行通行数量/业务请求候选 |
| `SwingGate::serialSendPer_replaceEth` | `0xb7150` | 串行替换/更新业务请求候选 |

## 反馈和 UI 路径

| 符号 | 地址 | 已确认职责 | 重要字段 |
|---|---:|---|---|
| `Form::CanGateBack5` | `0x5e230` | 处理事件 `0x14/0x73/0x74`，更新计数/UI和队列状态 | `0x14` 至少读取 payload[0..2]；payload[2] 非零时进入通行数量路径 |
| `Form::CanGateBack6` | `0x5db1c` | 处理反馈首字节 `C6/C8/CC/E2/E4` | 多个分支把 payload[2:4] 解析为大端数值 |
| `Form::SwingGateBack7` | `0x5d3b4` | 同类反馈的模式化处理，实际符号不是 `CanGateBack7` | `this+0x1f4` 模式守卫；仍是计数/UI/声音路径 |
| `HsSaveTicketNumEth` | `0x52a4c` | 保存票数/计数状态 | 第二参数 2/3 的厂商含义待定 |
| `PlayWav` | `0x3f644` | `CC` 条件下播放预置音频 | 音频编号 `0x6d` 的业务提示待定 |
| `Form::CanGateCheckPassNum` | `0x1bb6c` | 计数差异检查/补偿调用点 | 是否代表控制板确认，不得由函数名推断 |
| `Form::BarCodeRead` | `0x615d4` | 原厂扫码结果进入票务/UI处理的主函数 | 串口帧字段与云端查询的完整映射 |
| `Form::CanBusInit` | `0x32270` | 初始化 CAN 业务线程和接口配置 | 现场启动时序与失败回退 |

## 重点函数的字段注释

### `canSendPer_addEth(0xcdbd4)`

```text
payload[0] = flag != 0 ? 0xC5 : 0xC7
payload[1] = 0x03
payload[2] = 0x01
payload[3] = count >> 8
payload[4] = count & 0xff
payload[5] = payload[0] ^ payload[1] ^ payload[2] ^ payload[3] ^ payload[4]
```

这是加密前候选 payload，不是 CAN 线上明文。`this+0x3c == 0` 时程序记录 commKey 不可用并退出发送路径。

### `canSendPer_replaceEth(0xcd798)`

该路径与 add 路径的字段位置相同，仅确认第三个业务字节不同：

```text
payload[0] = flag != 0 ? 0xC5 : 0xC7
payload[1] = 0x03
payload[2] = 0x00
payload[3] = count >> 8
payload[4] = count & 0xff
payload[5] = payload[0] ^ payload[1] ^ payload[2] ^ payload[3] ^ payload[4]
```

`CanBusThread::event` 将事件 `0x1f69` 路由到该函数；事件 `0x1f68` 路由到 `canSendPer_addEth`。这只是业务请求类型的静态差异，不能直接把 `replace` 命名为换票、补票或开闸动作。

### `CanDataDecrypt(0xccf10)`

反汇编可直接看到三组对象字段被用于恢复业务数据：

- `this+0x40`：已取得的 commKey；
- `this+0x20`：接收事务序号/`CommNo_Recv`；
- `this+0x48`：接收时间摘要/`randomData_Recv`。

函数以接收序号和摘要构造 MD5 掩码，然后按输入长度逐字节 XOR 恢复 payload，最后交给 CAN 事件链。这里能确认保护算法和字段来源，不能仅凭静态代码给解密后的事件码赋予“开闸成功”含义。

### `Form::CanGateCheckPassNum(0x1bb6c)`

函数先检查 `this+0x1f6 == 5`，随后创建一个约 0x5c 大小的事件对象，把调用参数写入事件偏移 `+0x1f`，数量字段 `+0x4e` 清零，模式/标志 `+0x50` 设为 1，再投递给 `this+0x284` 关联对象。它是计数检查/事件补偿入口，不是控制板物理 ACK。

### `CanGateBack6(0x5db1c)`

| 首字节 | 条件 | 直接动作 | 不能命名为 |
|---|---|---|---|
| `C6` | `payload[1]==2` | byte2/3 大端数值，保存票数/计数 | 杆已转动 |
| `C8` | `payload[1]==2` | byte2/3 大端数值，保存票数/计数 | 杆已转动 |
| `CC` | `payload[1]==1 && payload[2]==1` | `PlayWav(0x6d,0x320)` | 成功开闸 |
| `E2` | `payload[1]==2` | 复用数值保存路径 | 控制板 ACK |
| `E4` | `payload[1]==2` | byte2/3 大端数值，保存参数 3 | 通行完成 |

## 原始证据定位

- 目标反汇编：`../../artifacts/gate-original/disasm-targeted.txt`
- 反馈反汇编：`../../artifacts/gate-original/disasm-back6.txt`
- 发送链反汇编：`../../artifacts/gate-original/send-path-disasm.txt`
- 字符串和符号：`../../artifacts/gate-original/strings-relevant.txt`
- 汇总报告：`../../artifacts/gate-original/protocol-payload-analysis-20260823.md`
