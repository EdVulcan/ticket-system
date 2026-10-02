# 临时票券二维码工具

访问路径：`/temporary-qr/`

点击二维码可打开票据预览，并在手机或电脑上直接保存为 PNG 图片。

## 部署前配置

1. 此工具不设置访问密码，打开页面即可管理二维码。请仅通过不公开该网址的方式控制访问范围。
2. 重新执行管理端和后端构建，后端会自动托管该目录。

票据初始数据在 `tickets.json`，静态路由不公开该文件。状态保存在服务端 `data/temporary-qr.json`，可用 `TICKET_SERVER_TEMPORARY_QR_STORE_PATH` 覆盖。现有 CI 将 data 链接到 `/var/lib/ticket-system`，状态保留跨部署。所有终端每 5 秒同步，写入时版本校验避免旧数据覆盖。

## 完整移除

移除 `admin/public/temporary-qr` 和 `backend/cmd/temporary_qr.go`，以及 `backend/cmd/main.go` 中的 `registerTemporaryQRAPI` 调用、`serveTemporaryQRTicketManager` 调用和函数即可。运行时数据可另行归档。正式业务路由、模型、数据库和 Vue 页面不依赖此工具。
