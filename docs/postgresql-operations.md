# PostgreSQL 运维说明

## 1. 当前架构

生产数据库使用 PostgreSQL，本轮代码 schema 版本为 117（待发布）。Go 服务、管理端静态资源和业务任务仍由一个应用进程承载，不引入 Redis、MySQL、消息队列或微服务。

schema 111 增加手机网页核销会话；schema 112 为现有小红书商品配置增加最近审核查询时间 `audit_checked_at`、查询错误 `audit_check_error` 和查询调度索引。升级保留原审核状态、审核原因及提交记录，不批量放行历史待审核商品；服务启动后按官方商品查询结果逐项更新。查询时间与小红书实际审核时间 `audited_at` 分开保存。

应用启动时必须拒绝高于自身支持版本的数据库 schema，避免旧程序在回滚后忽略新增的租户或业态授权边界。schema 80 首次上线属于前向迁移：部署前必须完成备份，不能直接回滚到不认识 `supplier_business_types` 的旧二进制。schema 91 会把旧平台 AI 配置中的 30 秒请求超时迁移为 120 秒，并更新列默认值；schema 93 会把旧 AI 工具事件的任务版本幂等键迁移为按调用尝试唯一，并将旧调用编号索引改为非唯一索引；schema 94 会把 DeepSeek 的旧默认 `legacy_json` 协议迁移为 `auto`，使新任务使用原生工具调用；schema 95 扩展 Agent 任务的 ownership guard 和基础信息预览/确认类型；schema 96 增加批量基础信息预览/确认操作类型；schema 97 增加租户业务别名表和 ownership guard；schema 98 注册复合低风险预览父任务类型，子任务继续复用现有确认和版本校验；schema 99 增加酒店价格计划入住日期覆盖表及联合归属约束；schema 100 增加独立酒店产品目录、产品售价日历、住宿权益/预订表及联合租户归属约束；部署前仍必须完成备份。

schema 101 将 Agent 任务创建幂等键固定为 `(tenant_id, actor_user_id, idempotency_key)`，schema 102 增加独立的租户 AI 额度策略表及归属/额度触发器；schema 103 增加租户/景区/票种范围的结构化门票打印模板、不可变模板版本、打印任务服务端内容快照及对应 ownership guard；schema 104 为模板、打印快照和打印任务增加竖版/横版方向；schema 105 为窗口订单增加租户/窗口渠道作用域的请求 ID、内容哈希和支付后打印恢复锁；schema 106 增加独立闸机维护凭据、短时维护会话和归属触发器；schema 107 增加一次性闸机安装绑定租约；schema 108 增加小红书券核销协调和票权占用；schema 109 增加小红书商品审核状态及待审核默认值；schema 110 增加小红书售后隔离协调、账号/整单阻断和管理员人工处置审计。策略的空上限继承平台默认值，暂停和额度调整不改写 `AIUsageMonth` 账本。PostgreSQL 负责并发事务和持久化，也是自动化测试的唯一数据库。项目不再包含 SQLite 驱动、运行配置、备份恢复或兼容测试。

Agent 边界补充（2026-08-17）：schema 95/96 的任务与 ownership guard 兼容现有任务结构；当前运行时允许当前租户自有票种（包括已上架或允许分销者）进行基础信息预览/确认，分销副本仍拒绝。该边界调整不改变迁移版本，也不允许修改上架、分销授权或跨租户归属事实。

## 2. 数据库账号

建议为应用创建专用登录账号和专用数据库。应用账号应拥有目标数据库及 `public` schema 中的表、序列、函数和触发器，不应授予超级用户权限。

默认连接参数：

| 配置 | 默认值 | 环境变量 |
|---|---|---|
| 主机 | `127.0.0.1` | `TICKET_DATABASE_HOST` |
| 端口 | `5432` | `TICKET_DATABASE_PORT` |
| 数据库 | `ticket_system` | `TICKET_DATABASE_NAME` |
| 用户 | `ticket_app` | `TICKET_DATABASE_USER` |
| 密码 | 无 | `TICKET_DATABASE_PASSWORD` |
| SSL | `disable` | `TICKET_DATABASE_SSLMODE` |
| 时区 | `Asia/Shanghai` | `TICKET_DATABASE_TIME_ZONE` |

生产环境可用 `TICKET_DATABASE_URL` 覆盖上述连接字段。密码只放在部署环境或密钥管理系统中，不写入 YAML、日志或 Git。

## 3. 初始化与升级

数据库和账号准备好后执行：

```powershell
cd backend
$env:TICKET_DATABASE_PASSWORD = '数据库账号密码'
go run ./cmd/db-migrate
```

命令可重复执行。新建 PostgreSQL 库会直接建立当前 schema、必要索引和跨租户/跨景区归属触发器。

服务发布时不需要先手动运行这个命令：服务会在绑定 HTTP 端口、启动后台任务或报告就绪前运行同一迁移入口。若已存在的 `schema_migrations` 最高版本大于 `0` 且低于当前程序支持版本，服务会先在 `data/backups/pre-upgrade`（生产环境映射为 `/var/lib/ticket-system/backups/pre-upgrade`）创建、校验 PostgreSQL custom dump，并复制同时间戳的实例密钥副本；只有备份成功才会变更 schema。该目录独立于周期备份目录，仍按 `backup.retention` 保留最近的升级前备份及其配对 `.key.json` 文件。新库、空迁移记录和已在当前版本的库不会触发这一步。

升级前备份或迁移任一步失败，服务会以非零状态退出，不能开始监听或报告 ready。数据库版本高于当前二进制支持的版本仍会明确拒绝启动，不能通过跳过备份来降级绕过。

旧系统或 SQLite 数据导入不属于本项目交付范围；如以后单独立项，应使用独立工具和脱敏样本处理，不恢复 SQLite 运行支持。

## 4. 自动备份

服务启动时立即调用 `pg_dump` 生成自定义格式 dump，此后按配置周期执行。每个备份包含：

- `ticket-system-pg-时间戳.dump`
- `ticket-system-pg-时间戳.key.json`

实例密钥必须与同时间戳 dump 成对保存，否则加密配置可能无法解密。`pg_dump`/`pg_restore` 可放在 `PATH`，也可通过 `TICKET_BACKUP_POSTGRES_BIN_DIR` 指定安装目录。

旧版本升级前的同步备份设有 2 分钟总超时，涵盖 `pg_dump` 与 `pg_restore --list` 校验。超时取消子进程并拒绝执行迁移，服务以非零退出码结束；应排查数据库锁或备份环境后重试，不允许跳过备份继续升级。普通定时备份的周期不变。

主线 GitHub Action 上传 release 并调用服务器上的 `/usr/local/sbin/ticket-system-deploy`；仓库工作流不会直接访问生产数据库或备份目录。实际 schema 迁移和上述升级前备份由新服务启动完成。激活之后 Action 请求 `/api/v1/ready`，精确比对本次 Git SHA 与源码声明的 schema 版本；接口只在数据库版本匹配时返回成功，检查失败会阻止部署任务报告成功。即使发布成功，数据库的前向迁移也不能靠切回旧二进制回滚。需要回退时，停止服务，使用对应的 `pre-upgrade` dump 与配对 key 恢复，再启动与该 dump schema 兼容的版本。

schema 117 增加上游供应预置。已有产品不激活任何上游映射；每个已有订单明细补建一份 `local` 供应快照，包括已支付、已核销、已退款、已关闭及软删除明细，不修改原始业务记录。所有 DDL、回填及版本标记在 PostgreSQL advisory lock 保护的事务内提交。无法关联原订单的孤儿明细会令整批迁移回滚，需依据原始归属处理后重试；不自动删除旧数据，也不猜测供应归属。真实上游凭证写入和新售启用仍关闭。

## 5. 恢复

恢复是运维操作，执行前必须停止应用，避免连接池持有旧状态：

```powershell
cd backend
$env:TICKET_DATABASE_PASSWORD = '数据库账号密码'
go run ./cmd/restore `
  --source-dump data/backups/ticket-system-pg-时间戳.dump `
  --source-key data/backups/ticket-system-pg-时间戳.key.json `
  --target-key data/instance-key.json `
  --rollback-dir data/backups
```

工具会先校验 dump，再生成 `ticket-system-before-restore-时间戳.dump`，最后以单事务清理并恢复目标数据库。恢复失败时保留回滚 dump，不报告假成功。恢复后应执行：

1. `go run ./cmd/db-migrate`，确认 schema 为当前版本。
2. 启动服务并验证平台、供应商、分销商和旅行社登录。
3. 抽查订单、票权益、核销、退款和结算数据。
4. 在业务恢复前记录恢复耗时和验证结果。

## 6. 测试库与 CI

普通测试使用具备 `CREATEDB` 权限的 PostgreSQL 测试账号，按包创建随机命名的隔离数据库并在结束后清理。备份恢复集成测试固定使用：

- `ticket_system_test`
- `ticket_system_restore_test`

测试会修改和清理这些数据库，严禁将测试变量指向生产库。CI 使用独立 PostgreSQL 16 服务，运行核心业务、租户隔离、并发事务和真实 `pg_dump`/`pg_restore` 演练。

当前没有启用 PostgreSQL RLS。租户隔离由服务层强制作用域、数据库归属触发器和跨租户负向测试共同保障；在没有明确多实例直连或数据库侧租户账号需求前，不增加 RLS 策略复杂度。

## 7. 生产发布目录与上传文件

生产发布包中的 `backend` 目录属于版本化 release，服务不得把运行时数据直接写入该目录。systemd 当前以 `ProtectSystem=strict` 运行服务，仅开放 `/var/lib/ticket-system` 和日志目录写入；主线 CI 发布包会把 `backend/data` 映射到 `/var/lib/ticket-system`，该目录用于保存实例密钥、PostgreSQL 备份和小红书商品图片。服务器需预先创建该目录并赋予部署账号/服务账号所需的既有权限；发布步骤只创建缺失的 `uploads`、`backups` 子目录，不尝试修改运行时根目录的属主或模式，再按“不覆盖已有文件”迁移旧版本数据并执行可写性检查。

如果部署环境不是由仓库中的主线 CI 发布，必须在服务启动前完成等价配置：让服务账号对 `/var/lib/ticket-system` 及其子目录拥有读写权限，并确保 `backend/data` 不落在只读 release 文件系统内。商品图片上传接口返回成功前会写入该目录；小红书同步使用的 HTTPS 图片地址随后必须能通过 `/api/v1/public/channel-product-images/{tenant}/{account}/{filename}` 访问。目录不可写时，系统应保持同步失败并记录原因，不能把本地保存失败报告为商品发布成功。
