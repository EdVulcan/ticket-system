# 逆向注释覆盖范围

这个文件防止把“核心链路已经注释”误认为“整个 ELF 的每条指令都已命名”。当前目录覆盖的是与扫码、票务确认、CAN 事务、反馈、启动和共存直接相关的代码；未命名的普通 Qt/UI、数据库和厂商工具函数仍保持原始反汇编状态。

## 原始材料到专题文档的映射

| 原始材料 | 主要内容 | 已归档到 |
|---|---|---|
| `disasm-targeted.txt` | 目标函数和主调用链 | `FUNCTIONS.md`、`PROTOCOL.md` |
| `send-path-disasm.txt` | CAN 发送、加密前 payload、0x122/0x123 | `FUNCTIONS.md`、`PROTOCOL.md` |
| `disasm-back6.txt` | C6/C8/CC/E2/E4 反馈分支 | `FUNCTIONS.md`、`PROTOCOL.md` |
| `swing-related.txt` | SwingGateSetper、SwingGateBack7 | `FUNCTIONS.md`、`PROTOCOL.md` |
| `helper-disasm.txt` | 校验、加密、CAN 辅助函数 | `PROTOCOL.md`；未命名部分待补 |
| `literal-analysis.txt` | 字符串、常量和 CAN ID 交叉证据 | 相关函数条目的证据链接 |
| `strings-relevant.txt` | 原厂日志、配置和符号文字 | `FUNCTIONS.md`、`RUNTIME.md` |
| `can-*.log`、`joint-*.md` | 现场被动样本 | `SAMPLES.md` |
| `tools/can_fixture_parser.py`、`tools/test_can_fixture_parser.py` | 被动样本的帧结构与四帧事务回归 | `SAMPLES.md`、`PROTOCOL.md` |

## 当前覆盖等级

- **已注释（核心）**：有效票入口、`0x1f68/0x1f69` add/replace 事件、C5/C7 候选 payload、CAN 路径 `getCommKeyTimerOut(0xcc414)`、P07=6 的 `CanBusThread::run` 及 `0x121/0x122/0x123` 分支、`CanDataDecrypt(0xccf10)`、P07=5 的 `proceedCanMessage(0xcf2c4)`、C6/C8/CC/E2/E4 反馈、启动脚本和 gate-client 共存边界。
- **部分注释**：DES 通用封装、数据库查询和 UI 计数的调用者；P07=6 的目标 Qt4
  版本尚未现场只读核对，且控制板物理反馈语义未闭合；但按标准 Qt4 ABI，
  `CanBusThread+0x8` 是构造时传入的 `Form*` 父对象，`UserEvent(0x1f6a) →
  Form::event → Form::CanGateBack6` 的 ELF 跳转表也已静态闭合。只记录对协议或
  现场排障有影响的字段。
- **未注释语义**：其余 Qt 窗口、演示程序、厂商配置页、非闸机业务工具和未触发的异常分支。

未覆盖区域不能作为协议证据，也不能因为存在同名字符串就推断物理动作。新增函数时应先在 `FUNCTIONS.md` 添加地址和证据等级，再决定是否拆出专题文档。

## 覆盖完成标准

对“真实三辊闸驱动”而言，覆盖完成不是把所有 Qt 函数都翻译成中文，而是能为每个主动 CAN 帧回答：

1. 谁触发、输入字段来自哪里；
2. 完整 payload 和保护方式；
3. 控制板返回什么、由哪个函数处理；
4. 哪个证据证明业务 ACK，哪个证据证明杆已转动；
5. 失败、超时、断电和重试如何恢复。

只要其中一项缺失，该帧保持“未闭合”，gate-client 不实现真实开闸。
