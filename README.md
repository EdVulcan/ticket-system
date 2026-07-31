# 景区票务系统

这是一个可独立运行的景区票务服务。管理端静态页面、Go API、SQLite 数据库和定时备份由同一个服务承载，运行时不需要 MySQL、Redis 或单独的 Web 服务器。

## 开发基线文档

涉及租户、商品、订单、库存、核销、分销、渠道、支付或资金的开发前，请先阅读：

- [多租户景区票务平台开发基线与演进指南](docs/platform-multitenancy-development-guide.md)
- [景区票务经营系统产品研究与当前项目差距](docs/scenic-ticketing-product-research.md)

## 快速启动

从发布目录启动时，只需运行一个进程：

```powershell
cd release/backend
$env:TICKET_BOOTSTRAP_ADMIN_PASSWORD = '请设置一个强密码'
$env:TICKET_BOOTSTRAP_PLATFORM_USERNAME = 'platform_admin'
$env:TICKET_BOOTSTRAP_PLATFORM_PASSWORD = '请设置另一个不同的强密码'
.\ticket-system.exe
```

首次启动且数据库中没有用户时，服务会创建以下管理员：

- 商户系统编号：`SYS001`
- 用户名：`admin`
- 密码：环境变量 `TICKET_BOOTSTRAP_ADMIN_PASSWORD` 的值

平台治理账号与租户管理员完全独立：

- 用户名：环境变量 `TICKET_BOOTSTRAP_PLATFORM_USERNAME` 的值
- 密码：环境变量 `TICKET_BOOTSTRAP_PLATFORM_PASSWORD` 的值，且不能与租户管理员密码相同

平台账号只用于平台登录模式和租户治理；租户业务能力默认按实际历史数据迁移，新租户的供应商、分销商和旅行社能力必须由平台明确开通。

随后访问 `http://127.0.0.1:8080/`。管理员创建成功后，后续启动不再需要保留该环境变量。

## 构建发布包

构建环境需要 Go、Node.js 22，以及启用 CGO 的 GCC。Windows 推荐安装 MSYS2 UCRT64 GCC：

```powershell
pacman -S --needed mingw-w64-ucrt-x86_64-gcc
```

在仓库根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

脚本会重新安装前端锁定依赖、构建管理端，并生成：

```text
release/
  admin/dist/                  管理端静态文件
  backend/config/config.yaml  服务配置
  backend/ticket-system.exe   唯一需要运行的服务
```

## 数据与备份

以 `release/backend` 为工作目录启动时：

- 主数据库：`data/ticket-system.db`
- 实例密钥：`data/instance-key.json`
- 自动备份：`data/backups/`
- 日志：`app.log`

服务启动时立即备份，此后默认每 24 小时备份一次，保留最近 14 组。每组由同一时间戳的 `.db` 和 `.key.json` 两个文件组成。

恢复前必须停止服务，并成对恢复数据库和实例密钥：将选定的 `.db` 放回 `data/ticket-system.db`，将同名的 `.key.json` 放回 `data/instance-key.json`。不要混用不同时间戳的文件。

端口、并发参数、备份周期和保留数量可在 `release/backend/config/config.yaml` 中调整，也可以用 `TICKET_` 前缀的环境变量覆盖。

## 开发验证

后端使用 CGO。在 PowerShell 中可执行：

```powershell
$env:Path = 'C:\msys64\ucrt64\bin;' + $env:Path
$env:CGO_ENABLED = '1'
cd backend
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

前端分别在 `admin` 和 `desktop` 目录执行：

```powershell
npm ci
npm audit
npm run build
```
