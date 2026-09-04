# 原厂闸机发送链等价伪代码

更新时间：2026-08-24

本文把 ARMv7 原厂程序中目前已经有静态证据支持的发送链压缩成等价伪代码，
用于后续实现自己的 `gate-client`。它不是原厂源码，也不把函数名推断成物理
动作。所有现场发送前，必须先完成离线 fixture 和隔离台架验证。

## 1. 证据等级

- **A**：反汇编直接读写字段、调用带符号函数或写入明确 CAN ID。
- **B**：调用链、字符串和现场被动样本共同支持，但厂商业务语义未确认。
- **C**：函数名或时序推测，只能用来安排下一步只读验证，不能用来发帧。

## 2. 已确认的业务事务链

```text
票务确认
  -> SwingGateSetper(count, flag)
  -> UserEvent(0x1f68)
  -> CanBusThread::canSendPer_addEth
  -> C5/C7 明文候选载荷 + XOR
  -> commKey/序号/时间摘要保护
  -> CAN 0x122 握手帧
  -> CAN 0x123 业务帧
  -> 控制板后续响应
  -> CanDataDecrypt
  -> UserEvent(0x1f6a)
  -> Form::CanGateBack6(payload)
```

现场有效票产生过四帧 `0x122 -> 0x123 -> 0x122 -> 0x123`，无效票没有 CAN
帧。这支持“核票通过后才进入 CAN 事务”，但没有方向标记，不能单凭顺序把某帧
命名为控制板 ACK 或开闸完成。

## 3. 票务确认到业务载荷

来源：`Form::SwingGateSetper=0x3434c`、
`CanBusThread::event=0xce7bc`、`canSendPer_addEth=0xcdbd4`。

```text
onTicketConfirmed(count, flag):
    event.type  = 0x1f68
    event.count = count       # event 偏移 +0x4e
    event.flag  = flag        # event 偏移 +0x1f
    canBus.handle(event)

canSendPerAddEth(count, flag):
    if not commKeyReady:
        log("commKey unavailable")
        return

    prefix = 0xC5 if flag != 0 else 0xC7
    plain = bytes([
        prefix,
        0x03,
        0x01,
        (count >> 8) & 0xff,
        count & 0xff,
    ])
    plain.append(XORVerify(plain[0:5]))
    sendProtectedTransaction(plain)
```

`canSendPer_replaceEth=0xcd798` 使用相同位置，但第三字节为 `0x00`。`C5/C7`
载荷只是加密前的业务候选，不能直接作为 `cansend` 参数。

## 4. 通信密钥和 0x122/0x123

来源：`getCommKeyTimerOut=0xcc414`、`CanDataEncrypt=0xcc534`、
`CanDataDecrypt=0xccf10`。

```text
requestCommKey():
    sendCAN(id=0x121, payload=empty_8_bytes)

onCommKeyResponse(payload):
    commKey = payload
    commKeyReady = true

sendProtectedTransaction(plain):
    sendSequence += 1
    sequence = long2Byte(sendSequence)
    timeDigest = MD5(now("yyyyMMddhhmmsszzz"))[0:4]

    sendCAN(id=0x122, payload=sequence + timeDigest)

    mask = MD5(commKey + sequence + timeDigest + "SENDINFO")
    protected = XORWithMask(plain, mask)
    sendCAN(id=0x123, payload=protected)
```

上面的字节序、分片和 `XORWithMask` 的完整实现仍应以反汇编中的逐字节逻辑和
离线样本为准；本文件不提供可直接写入现场 CAN 的帧。

## 5. 队列/杆相关候选（尚未证明是当前开闸入口）

来源：`CanBusThread::setPoleUpDown=0xce038`、
`canSendCmdQueueZ=0xcba64`、`canSendCmdQueueF=0xcbbf0`。

2026-08-24 对整个可加载代码段做了 ARM 直接 `BL` 交叉引用扫描：没有找到
任何指向 `setPoleUpDown(0xce038)` 的直接调用。该函数可能是未使用的遗留路径，
也可能通过尚未识别的间接调用进入；在补齐证据前只能按 **C 级候选** 处理。
`canSendCmdQueueZ/F` 的直接调用位于队列/`proceedCanMessage` 相关路径，不能自动
归属于当前 P07=6 的四帧事务。

```text
setPoleUpDown(value):                         # C：函数名暗示杆上下，未找到直接调用者
    addSendTimes()
    payload = bytes([0x00, value, 0x00, 0x00]) # A：仅 byte1 直接写入 value
    zQueue.append(payload)
    if zQueueCanBeSent:
        canSendCmdQueueZ()

canSendCmdQueueZ():
    sendCAN(id=0x142, payload=zQueue.front())  # A：ID，物理语义未确认

canSendCmdQueueF():
    sendCAN(id=0x152, payload=fQueue.front())  # A：ID，物理语义未确认
```

当前只能确认 `0x142/0x152` 是 Z/F 队列写入 ID；不能确认它们分别代表上行、
下行、显示同步、数量同步或开闸。`setPoleUpDown` 的参数值、完整 payload、
控制板响应和杆动作仍未闭合，因此不能据此生成现场命令。

因此，当前自己程序应优先复刻 `CanDataEncrypt -> 0x122/0x123` 的活动链，
而不是先尝试 `setPoleUpDown` 或 `0x142/0x152`。后者只有在找到真实调用者、
完整 payload 和控制板响应后，才进入隔离台架验证。

## 6. 接收链不能替代物理反馈

```text
onProtectedCanResponse(frame):
    payload = CanDataDecrypt(frame)
    postEvent(type=0x1f6a, payload=payload)

Form::CanGateBack6(payload):
    switch payload[0]:
        C6/C8/E2/E4 -> 保存票数/计数并更新界面
        CC           -> 播放声音
        otherwise    -> 忽略或记录
```

`CanGateBack6` 目前只证明了声音、数量和 UI/持久化路径；没有直接看到电机、
GPIO、杆位传感器或“通行完成”确认。因此 `payload` 被处理不能等同于
`opened=true`。

## 7. 自有程序的安全实现顺序

1. 将本文件中的 `0x121/0x122/0x123` 保护层和 C5/C7 载荷做成离线 parser/
   fixture，输入已保存的 `candump`，只验证解码和状态机，不写 CAN。
2. 静态追踪 `setPoleUpDown` 的所有调用者，确认它是在什么业务事件之后入队，
   并补齐 `0x142/0x152` 的完整载荷来源。
3. 现场只做被动监听：原厂有效票扫码时同时记录时间、CAN ID、原厂屏幕计数、
   杆是否转动和完成时间；不要抢占扫码串口。
4. 只有取得厂商协议或隔离控制板后，才在断开机械执行机构/安全台架上验证单帧
   命令。未知 payload、未知方向和未知反馈一律不发送。
5. 最终自己的客户端必须把云端允许、硬件已打开、硬件失败和结果未知分开记录，
   不能把“帧已发出”或“服务返回 2xx”当成物理开闸成功。

## 8. 证据来源

- `artifacts/gate-original/protocol-payload-analysis-20260823.md`
- `artifacts/gate-original/p07-6-receiver-analysis-20260824.md`
- `docs/gate-original-program-analysis.md`
- `artifacts/gate-original/joint-can-20260823.log`
