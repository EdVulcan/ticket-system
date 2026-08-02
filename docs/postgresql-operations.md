# PostgreSQL 运维说明

## 1. 当前架构

生产数据库已切换到 PostgreSQL，当前 schema 版本为 61。Go 服务、管理端静态资源和业务任务仍由一个应用进程承载，不引入 Redis、MySQL、消息队列或微服务。

PostgreSQL 负责并发事务和持久化。SQLite 仅保留用于快速单元/回归测试及未来可能的旧数据导入验证，不再作为生产数据库。

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

命令可重复执行。新建 PostgreSQL 库会直接建立当前 schema、必要索引和跨租户/跨景区归属触发器，不重放包含 SQLite 专用语法的历史迁移。

本次迁移检查没有发现正式业务 SQLite 数据库，因此没有执行数据导入，也没有创建未经真实样本验证的通用转换器。以后如确有旧数据导入需求，应以真实脱敏样本单独设计、审计和验收，不在原库上试验。

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
go run ./cmd/restore --driver postgres `
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

本地 PostgreSQL 集成测试使用：

- `ticket_system_test`
- `ticket_system_restore_test`

测试会修改和清理这些数据库，严禁将测试变量指向生产库。CI 使用独立 PostgreSQL 16 服务，除 SQLite 全量回归外，还运行核心业务服务测试和真实 `pg_dump`/`pg_restore` 演练。

当前没有启用 PostgreSQL RLS。租户隔离由服务层强制作用域、数据库归属触发器和跨租户负向测试共同保障；在没有明确多实例直连或数据库侧租户账号需求前，不增加 RLS 策略复杂度。
