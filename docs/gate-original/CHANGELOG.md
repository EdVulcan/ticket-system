# 逆向研究记录

## 2026-08-24

- 通过 `Form::event(0x755c0)` 的自定义事件跳转表闭合当前 P07=6 接收上层：
  `CanDataDecrypt(0xccf10) → UserEvent(0x1f6a) → event+0x28 payload →
  Form::CanGateBack6(0x5db1c)`。`0x5c` 仍只是对象分配大小，不是事件类型。
- 按标准 Qt4 `QObjectData` ABI（虚析构表后 `parent` 位于 `d_ptr+8`）补充 receiver
  归属：原厂构造时传入的 `Form*` 被缓存到 `CanBusThread+0x8`；目标 Qt4 版本仍需
  现场只读核对。继续保持物理边界：`CanGateBack6` 的计数、声音或 UI 更新不能命名
  为控制板 ACK、杆已转动或通行完成。
- 将上述闭合结果同步到 `PROTOCOL.md`、`FUNCTIONS.md`、`OPEN-QUESTIONS.md`、
  `COVERAGE.md` 及主线分析文档，移除会误导后续工作的“P07=6 接收对象完全未知”旧表述。

## 2026-08-23

- 建立独立逆向研究库，分离总览、函数注释、CAN 协议、运行时、样本和未闭合问题。
- 静态确认有效票链路：`0x1f68 → canSendPer_addEth → C5/C7 + XOR → commKey → 0x122/0x123`。
- 明确 `0x121` 为 commKey 请求/握手路径，`0x142/0x152` 仍只是 Z/F 队列发送 ID。
- 整理 `CanGateBack5/6` 和 `SwingGateBack7` 的字段条件，禁止把计数/UI/声音路径命名为物理开闸完成。
- 补充 `/dev/urandom`、S98、`start_a9.sh`、watchdog 和 `ticket-gate` 共存风险。
- 保留原厂备份 SHA-256：`CB8766B67382559CFF64319A2F0E43052820A3A359C1C5EF463615EE41191D9D`。
- 补齐 `CanDataDecrypt(0xccf10)`、`CanGateCheckPassNum(0x1bb6c)` 和 `canSendPer_replaceEth(0xcd798)` 的地址/字段注释；确认 replace 使用第三业务字节 `00`，add 使用 `01`。
- 增加离线 CAN fixture parser 与 `unittest`：固定现有有效票四帧事务、两次感应的 8 帧样本及无扫码 0 帧基线；明确 parser 不解密、不连接 `can0`、不发送帧。
- 通过 `CanBusThread::run(0xcfc18)` 重新核对接收模式：当时的 P07=6 SocketCAN 分支在 `0x123` 后调用 `CanDataDecrypt`，构造业务事件类型 `0x1f6a` 的 `UserEvent`（`0x5c` 只是对象分配大小）并投递到 `CanBusThread+0x8`；`proceedCanMessage(0xcf2c4)` 的直接调用点属于 P07=5/AppCanRece 分支。该阶段文档曾保留 P07=6 接收对象缺口，已由 2026-08-24 的 `Form::event` 跳转表交叉引用补充闭合。

后续每次现场采样或静态分析都应追加日期、输入材料、证据等级和未决影响，不能覆盖历史结论。

## 2026-08-23（标注纠正）

- 修正 commKey 函数归属：`CanBusThread::getCommKeyTimerOut` 为 `0xcc414`，`0xb4dbc` 是 `SwingGate` 串行路径。
- 在函数索引和 CAN 协议文档中分离 CAN 与串行两条路径，避免把串行函数当作 CAN 握手证据。
