# 景区票务系统

这是一个面向平台方、景区供应商、分销商和旅行社的多租户票务系统。当前技术形态为 Go 模块化单体、Vue 管理端和 Wails v2 窗口端。生产数据库使用 PostgreSQL；不依赖 Redis、MySQL、消息队列或独立 Web 服务器。

## 开发基线

涉及租户、商品、订单、库存、核销、分销、渠道、支付或结算的开发前，请先阅读：

- [多租户景区票务平台开发基线与演进指南](docs/platform-multitenancy-development-guide.md)
- [当前开发推进路线](docs/current-development-roadmap-2026-08-01.md)
- [PostgreSQL 运维说明](docs/postgresql-operations.md)

## 运行条件

- PostgreSQL 16 或更高版本。
- 应用数据库和专用数据库账号。账号只需拥有该数据库及其 `public` schema，不应使用 PostgreSQL 超级用户运行应用。
- `pg_dump` 和 `pg_restore`。将其加入 `PATH`，或设置 `TICKET_BACKUP_POSTGRES_BIN_DIR`。

默认配置连接 `127.0.0.1:5432/ticket_system`，数据库用户为 `ticket_app`。密码不写入仓库，通过环境变量提供：

```powershell
$env:TICKET_DATABASE_PASSWORD = '数据库账号密码'
```

也可以用完整连接地址覆盖各连接字段：

```powershell
$env:TICKET_DATABASE_URL = 'postgres://ticket_app:密码@127.0.0.1:5432/ticket_system?sslmode=disable'
```

首次运行前初始化当前数据库结构：

```powershell
cd backend
go run ./cmd/db-migrate
```

## 首次启动

数据库为空时，必须为租户初始管理员和平台管理员提供两个不同的强密码：

```powershell
cd backend
$env:TICKET_DATABASE_PASSWORD = '数据库账号密码'
$env:TICKET_BOOTSTRAP_ADMIN_PASSWORD = '租户初始管理员密码'
$env:TICKET_BOOTSTRAP_PLATFORM_USERNAME = 'platform_admin'
$env:TICKET_BOOTSTRAP_PLATFORM_PASSWORD = '平台管理员密码'
go run ./cmd
```

默认租户系统编号为 `SYS001`，租户管理员用户名为 `admin`。平台账号与租户账号相互独立。初始化成功后访问 `http://127.0.0.1:8080/`；后续启动不再需要保留两个初始化密码。

## 构建发布包

构建环境需要 Go、Node.js 22，以及用于 SQLite 兼容测试的 CGO/GCC。Windows 推荐 MSYS2 UCRT64 GCC：

```powershell
pacman -S --needed mingw-w64-ucrt-x86_64-gcc
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

发布目录中的 `ticket-system.exe` 仍是唯一的应用进程，但 PostgreSQL 是独立的基础服务。

## 备份与恢复

服务启动后立即执行一次 PostgreSQL 自定义格式备份，此后默认每 24 小时备份一次，保留最近 14 组。每组由同一时间戳的 `.dump` 和 `.key.json` 组成，默认保存在 `data/backups/`。

恢复前停止应用服务，并使用同组数据库和实例密钥：

```powershell
cd backend
go run ./cmd/restore --driver postgres `
  --source-dump data/backups/ticket-system-pg-时间戳.dump `
  --source-key data/backups/ticket-system-pg-时间戳.key.json `
  --target-key data/instance-key.json
```

恢复工具会先保存当前数据库的回滚 dump，再使用单事务恢复。完整步骤见 [PostgreSQL 运维说明](docs/postgresql-operations.md)。

## 开发验证

SQLite 继续用于快速回归和历史兼容测试；核心业务还必须在真实 PostgreSQL 测试库运行：

```powershell
$env:Path = 'C:\msys64\ucrt64\bin;' + $env:Path
$env:CGO_ENABLED = '1'
cd backend
go test ./... -count=1
go test -race ./... -count=1 -timeout 10m
go vet ./...

$env:PGPASSWORD = 'PostgreSQL测试账号密码'
$env:TICKET_TEST_POSTGRES = '1'
$env:TICKET_TEST_POSTGRES_BIN = 'F:\PGSQL\bin'
go test ./internal/service -count=1
go test ./internal/backup -run TestPostgresBackupAndRestore -count=1
```

管理端和窗口端构建、Playwright E2E 已接入 CI；只有前端代码或依赖变化时才需要在本地重复构建。
