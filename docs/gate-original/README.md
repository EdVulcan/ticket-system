# 原厂闸机程序逆向研究库

这是原厂闸机程序的长期逆向研究目录。它与产品开发文档分离，目标是保存“代码证据 → 业务字段 → 现场样本 → 工程决策”的可追溯链，避免主上下文反复加载整份反汇编，也避免把推断误写成协议事实。

## 阅读入口

按问题读取，不要一次加载所有原始反汇编：

1. [FUNCTIONS.md](./FUNCTIONS.md)：函数、地址、输入输出和证据等级索引。
2. [PROTOCOL.md](./PROTOCOL.md)：CAN ID、加密/校验、业务 payload 和反馈事件。
3. [RUNTIME.md](./RUNTIME.md)：BusyBox 启动、watchdog、共存和离线排障。
4. [SAMPLES.md](./SAMPLES.md)：现场被动样本、有效/无效票对照及采样格式。
5. [OPEN-QUESTIONS.md](./OPEN-QUESTIONS.md)：未闭合问题、下一次现场只读验证和开闸实现门槛。
6. [CHANGELOG.md](./CHANGELOG.md)：研究结论的时间线。
7. [COVERAGE.md](./COVERAGE.md)：原始反汇编与专题注释的覆盖映射，避免把部分闭合误认为全部完成。
8. [`tools/can_fixture_parser.py`](./tools/can_fixture_parser.py)：只读 candump 结构解析器；[`tools/test_can_fixture_parser.py`](./tools/test_can_fixture_parser.py) 是离线 fixture 回归。

本轮 P07=6 上层事件闭合的静态证据见
[`artifacts/gate-original/p07-6-receiver-analysis-20260824.md`](../../artifacts/gate-original/p07-6-receiver-analysis-20260824.md)。
它把 `UserEvent(0x1f6a)` 映射到 `Form::CanGateBack6`，但不把计数/UI处理当作物理杆反馈。

当前综合结论仍见：

- [原厂程序静态分析](../gate-original-program-analysis.md)
- [现场研究汇总](../gate-original-research-summary.md)
- [gate-client 边界复核](../../artifacts/gate-original/gate-client-boundary-review-20260823.md)

## 研究对象与完整性

- 原厂备份：`../../artifacts/gate-original/gate-original-20260823.tar.gz`
- SHA-256：`CB8766B67382559CFF64319A2F0E43052820A3A359C1C5EF463615EE41191D9D`
- 解包目录：`../../artifacts/gate-original/extracted/`
- 原始反汇编和字符串：`../../artifacts/gate-original/*.txt`

所有逆向结论都必须注明来源文件和地址；不直接编辑备份二进制或现场脚本。原始材料较大时，只在专题文档中引用必要的地址/片段，完整输出留在 `artifacts/gate-original/`。

## 证据等级

- **A：直接代码证据**：反汇编明确读写字节、比较常量、调用带符号函数或写入明确 CAN ID。
- **B：交叉证据**：A 级调用链与字符串、现场被动样本或配置共同支持，但厂商业务命名尚未确认。
- **C：工程假设**：由函数名、时序或经验推断，只能用于设计下一次验证，不能用于发送 CAN 或实现开闸。

任何条目如果从 A/B 变成 C，必须在变更记录中说明原因；任何“杆已打开/通行完成”结论必须有独立物理反馈，不能只凭 UI、计数或 HTTP 成功。

## 安全边界

- 只做静态分析和 CAN 被动监听。
- 不向 CAN 注入帧，不重放现场事务。
- 不停止、替换或修改原厂程序、扫码器配置或 watchdog。
- 原厂程序继续独占 `/dev/ttyUSB2`；不要用 `cat`、`strace` 或第二个程序抢占扫码串口。
- `gate-client` 在真实协议闭合前只负责心跳、云端核销幂等、状态恢复和诊断；真实驱动保持 `driver_configured=false`。

## 文档维护规则

每次新增发现按以下顺序更新：

1. 将原始输出放入 `artifacts/gate-original/`，记录命令、日期和 SHA-256（如适用）。
2. 在 `FUNCTIONS.md` 或 `PROTOCOL.md` 添加最小字段事实和证据链接。
3. 在 `SAMPLES.md` 记录现场样本及观察到的杆/屏幕结果。
4. 在 `OPEN-QUESTIONS.md` 关闭或拆分问题，不把未证实语义直接改成结论。
5. 更新 `CHANGELOG.md`，再由根文档引用当前状态。

目录文档优先保存稳定事实；临时推测、完整反汇编和大段日志不要复制进这里。

## 建议的最小上下文组合

- 追踪“扫码后 CAN 做了什么”：`README + FUNCTIONS + PROTOCOL + SAMPLES`。
- 排查“设备为什么离线/原厂桌面是否会被影响”：`README + RUNTIME + OPEN-QUESTIONS`。
- 评估“能不能实现真实开闸”：`README + PROTOCOL + FUNCTIONS + OPEN-QUESTIONS + COVERAGE`。
- 新增现场样本：只读 `README + SAMPLES + OPEN-QUESTIONS`，完成后再回写 `CHANGELOG`。

除非需要定位具体指令，否则不要加载 `artifacts/gate-original/disasm-*.txt`；这些文件是证据源，不是日常上下文。
