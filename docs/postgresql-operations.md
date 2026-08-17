# PostgreSQL 运维说明

## 1. 当前架构

生产数据库已切换到 PostgreSQL，当前 schema 版本为 98。Go 服务、管理端静态资源和业务任务仍由一个应用进程承载，不引入 Redis、MySQL、消息队列或微服务。

应用启动时必须拒绝高于自身支持版本的数据库 schema，避免旧程序在回滚后忽略新增的租户或业态授权边界。schema 80 首次上线属于前向迁移：部署前必须完成备份，不能直接回滚到不认识 `supplier_business_types` 的旧二进制。schema 91 会把旧平台 AI 配置中的 30 秒请求超时迁移为 120 秒，并更新列默认值；schema 93 会把旧 AI 工具事件的任务版本幂等键迁移为按调用尝试唯一，并将旧调用编号索引改为非唯一索引；schema 94 会把 DeepSeek 的旧默认 `legacy_json` 协议迁移为 `auto`，使新任务使用原生工具调用；schema 95 扩展 Agent 任务的 ownership guard，允许未上架、未分销票种的基础信息预览/确认操作；schema 96 增加批量未上架、未分销票种基础信息预览/确认操作类型；schema 97 增加租户业务别名表和 ownership guard；schema 98 注册复合低风险预览父任务类型，子任务继续复用现有确认和版本校验；部署前仍必须完成备份。

PostgreSQL 负责并发事务和持久化，也是自动化测试的唯一数据库。项目不再包含 SQLite 驱动、运行配置、备份恢复或兼容测试。

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

旧系统或 SQLite 数据导入不属于本项目交付范围；如以后单独立项，应使用独立工具和脱敏样本处理，不恢复 SQLite 运行支持。

## 4. 自动备份

服务启动时立即调用 `pg_dump` 生成自定义格式 dump，此后按配置周期执行。每个备份包含：

- `ticket-system-pg-时间戳.dump`
- `ticket-system-pg-时间戳.key.json`

实例密钥必须与同时间戳 dump 成对保存，否则加密配置可能无法解密。`pg_dump`/`pg_restore` 可放在 `PATH`，也可通过 `TICKET_BACKUP_POSTGRES_BIN_DIR` 指定安装目录。

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
