# 原厂 CAN 协议注释（只读研究）

## 1. 传输环境

- P07_GateComType=`6`：SocketCAN 路径。
- 接口：`can0`。
- 现场配置：500000 bit/s，单帧最多 8 字节。
- 有效扫码被动样本固定出现：`0x122 → 0x123 → 0x122 → 0x123`。
- 无效票样本：0 CAN 帧。

## 2. CAN ID 字典

| ID | 代码事实 | 当前命名 | 证据等级 | 禁止结论 |
|---:|---|---|---|---|
| `0x121` | `CanBusThread::getCommKeyTimerOut(0xcc414)` 发送；接收分支写入 commKey | CAN commKey 请求/握手 | A/B | 不是已证实的开闸帧 |
| `0x122` | 加密事务中发送/接收 4 字节序号+4 字节摘要 | 事务序号/随机摘要交换 | A/B | 不能单独重放 |
| `0x123` | 承载 MD5/XOR 保护的业务数据 | 业务数据承载 | A/B | 样本不是明文票码 |
| `0x142` | Z 队列写入 | Z 队列发送 ID | A | 不能称为开闸 |
| `0x152` | F 队列写入 | F 队列发送 ID | A | 不能称为关闸/反向 |
| `0x488` | `SetType/SetPer` 写入 | 配置/参数路径 | A | 不能称为动作命令 |

### 2.1 正常有效票路径与其他命令队列的边界

截至 2026-08-23，现场的每次有效票事务均为连续四帧：

```text
0x122 → 0x123 → 0x122 → 0x123
```

静态调用链同时把有效票确认后的 `0x1f68/0x1f69` 业务事件接到
`canSendPer_addEth/replaceEth → canSendBuf → CanDataEncrypt`，而
`CanDataEncrypt` 的底层事务 ID 是 `0x122/0x123`。因此当前证据支持：

- `0x122/0x123` 是正常有效票业务事务的核心受保护通道；
- `0x142/0x152` 只出现在解密后 `0x14/0x15` 的 Z/F 队列发送路径；
- 现有有效票样本没有出现 `0x142/0x152`，不能把它们作为正常票务放行的必需帧；
- `0x122/0x123` 也不能单独命名为“开闸脉冲”，因为控制板 ACK 与物理杆完成仍没有独立证据。

这一区分是证据边界，不是对厂商协议名称的猜测。原始被动样本可用
`tools/can_fixture_parser.py` 离线校验，解析器只验证帧结构，不解密、不写入 CAN。

## 3. 事务保护

### 3.1 commKey

CAN 路径的 `CanBusThread::getCommKeyTimerOut(0xcc414)` 创建 8 字节空 payload，置 `this+0x3d=1`，通过 `CanWrite` 发送 `0x121`。收到响应后把返回数据写入 `this+0x40`，清除请求中标志并置 `this+0x3c=1`。因此 `this+0x3c` 是“commKey 可用”守卫，`this+0x3d` 是“请求进行中”状态。`SwingGate::getCommKeyTimerOut(0xb4dbc)` 是另一条串行路径，不能混入本 CAN 字典。

### 3.2 0x122/0x123

`CanDataEncrypt`：

1. 递增发送序号，形成 `commNo_Send`。
2. 生成当前时间字符串，MD5 后取前 4 字节形成时间摘要。
3. 通过 `0x122` 发送序号和时间摘要。
4. 计算 `commKey + commNo_Send + randomData_Send + SENDINFO` 的 MD5。
5. 用 MD5 结果逐字节 XOR 业务 payload，经 `0x123` 发送。

接收方向由 `CanDataDecrypt` 用接收序号、摘要和 commKey 逆转。当前 P07=6
SocketCAN 分支随后把恢复结果复制到新建 `UserEvent` 的 `event+0x28`，构造参数中的
业务事件类型字面量为 `0x1f6a`（`0x5c` 只是 `operator new` 的对象大小；
`UserEvent` 构造函数 `0x17080` 将 QEvent 类型固定为 `0x3e8`），再投递给
`CanBusThread+0x8` 指向的对象。结合标准 Qt4 `QObjectData` 布局（vptr `+0`、
`q_ptr +4`、`parent +8`），以及构造时传入的 `Form*` 父对象，`this+8` 可静态
归因为该 `Form*`。新增的跳转表交叉证据确认：`Form::event(0x755c0)`
按 `[event+0xc]` 将 `0x1f6a` 映射到 case `0x76384`，并直接调用
`Form::CanGateBack6(0x5db1c)`，payload 来自 `event+0x28`。因此 P07=6 的
“解密 → 上层反馈处理”已在 ELF 内静态闭合；目标现场仍可只读核对具体 Qt4 版本，
但不能由该函数推断控制板 ACK、三辊闸杆转动或通行完成。

`CanDataDecrypt(0xccf10)` 的字段来源已在反汇编中确认：`this+0x40` 为 commKey，`this+0x20` 为接收事务序号，`this+0x48` 为接收时间摘要。函数按输入长度逐字节 XOR 恢复 payload；恢复后的首字节仍需经过事件分派，不能直接当作物理开闸结果。

## 4. 业务 payload

加密前候选 payload（6 字节）：

```text
flag != 0: C5 03 01 count_hi count_lo xor
flag == 0: C7 03 01 count_hi count_lo xor
```

其中 `xor` 是前五字节逐字节 XOR。它不是随机数，不等于线上 `0x123` 的最终帧。`canSendPer_replaceEth(0xcd798)` 使用同样布局，但第三个业务字节为 `00`；`canSendPer_addEth(0xcdbd4)` 使用 `01`。事件 `0x1f68`/`0x1f69` 分别进入 add/replace 路径。

## 5. 反馈和另一条接收模式

`proceedCanMessage` 的已确认分派（**P07=5 / AppCanRece 分支**）：

| 事件 | 处理 |
|---:|---|
| `0x71` | 直接上抛 `canEvent` |
| `0x73`/`0x74` | 直接上抛并进入数量/UI相关路径 |
| `0x14` | 处理 Z 队列，可能继续 `0x142`；上抛 `canEvent` |
| `0x15` | 处理 F 队列，可能继续 `0x152`；上抛 `canEvent` |

反馈函数处理 `C6/C8/CC/E2/E4`。已确认的是条件、byte2/3 大端数值、计数保存和声音调用；厂商语义、控制板 ACK 与物理杆完成信号尚未命名。

这组 `0x14/0x15/0x71/0x73/0x74` 分派不能直接套到当前 P07=6：类型 6 的
`run` 只在 `0x123` 分支调用 `CanDataDecrypt`，随后投递 `UserEvent(0x1f6a)`；
反汇编中没有从该分支直接调用 `proceedCanMessage` 或 `canEvent` 的指令。

Z/F 队列发送函数会取队首 payload 调用 `CanWriteByLib`，分别写 `0x142`/`0x152`，并配合约 1000 ms 定时器处理队列结果；代码未显示杆位置传感器读取，故这里只能记录为“队列发送/响应调度”。

事件入口 `CanBusThread::event(0xce7bc)` 的跳转表以 `0x1f5e` 为基准，直接确认 `0x1f68 → canSendPer_addEth(0xcdbd4)`、`0x1f69 → canSendPer_replaceEth(0xcd798)`；两者都从事件结构读取数量 `+0x4e` 和 flag `+0x1f`。这是发送侧业务事件；接收侧 `proceedCanMessage` 的直接调用点仅在 P07=5 分支，不能代替当前 P07=6 的 `UserEvent(0x1f6a)` 接收映射。

`CanDataDecrypt` 完成的是业务 payload 恢复。P07=6 路径随后投递
`UserEvent(0x1f6a)`，并由 `Form::event(0x755c0)` 的跳转表 case `0x76384`
调用 `Form::CanGateBack6(0x5db1c)`；P07=5 路径才由 `proceedCanMessage(0xcf2c4)`
按事件首字节协调队列并上抛 `canEvent`。两条实现不能混写。两条路径都没有独立
读取杆位置传感器的证据，因此不能把任一事件命名为“闸杆已经完成通行”。

## 6. 当前协议闭环状态

```text
[已闭合] 有效票确认
    ↓
[已闭合] 0x1f68 → C5/C7 候选 payload
    ↓
[已闭合] commKey + 0x122/0x123 保护事务
    ↓
[已静态闭合] P07=6 解密后 UserEvent(0x1f6a) → Form::event → CanGateBack6
[另一条路径] P07=5/AppCanRece → proceedCanMessage → canEvent/队列
    ↓
[未闭合] 控制板 ACK、反馈码业务语义与实际三辊闸杆动作
```

在最后一层闭合前，禁止 CAN 注入、禁止复刻发送帧、禁止把 `allowed=true` 映射成 `opened=true`。

## 7. CAN 与串行路径边界

备份同时包含 `CanBusThread` 和 `SwingGate` 两套实现。`CanBusThread` 使用 `can0`/SocketCAN；`SwingGate` 使用 `serialSendBuf`、`serialDataEncrypt` 等串行路径。两套实现共享部分业务命名，但不共享可直接重放的 payload。后续分析必须在每条证据上注明类名，避免把 `SwingGate::getCommKeyTimerOut(0xb4dbc)` 误标为 CAN commKey。
