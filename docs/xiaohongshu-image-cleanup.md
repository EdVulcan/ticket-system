# 小红书图片孤儿清理维护命令

该命令用于处理小红书图片上传后未被配置保存的文件。默认是只读预览；只有显式传入 `--apply` 才会移动文件。命令没有永久删除操作，也没有 HTTP 入口或启动时自动任务。

## 使用

从 `backend` 目录执行：

```text
go run ./cmd/xhs-image-cleanup
go run ./cmd/xhs-image-cleanup --apply
```

默认读取现有 `config/config.yaml` 和 `TICKET_` 环境变量。也可以明确指定配置文件或上传目录：

```text
go run ./cmd/xhs-image-cleanup --config-file /etc/ticket-system/config.yaml --upload-directory /var/lib/ticket-system/uploads
go run ./cmd/xhs-image-cleanup --apply --min-age 336h
```

`--min-age` 的最小值是 `168h`（7 天），文件年龄按文件系统修改时间计算。维护命令是全局操作，不接受租户参数；输出 JSON 只包含扫描统计和相对路径，不包含数据库密码、JWT、加密密钥或渠道密钥。

## 扫描与保护规则

只检查以下精确路径：

```text
uploads/channel-products/<数字tenant>/<数字account>/<32hex>.png
uploads/channel-products/<数字tenant>/<数字account>/<32hex>.jpg
```

租户和账号目录使用上传器生成的非零十进制形式。其他目录、扩展名、文件名、额外层级、非普通文件和符号链接都会跳过；不会递归扫描 `uploads` 的其他目录。部署中配置的上传根目录可以是持久化目录链接，但图片路径本身及隔离目录组件不能是符号链接。

只有同时满足以下条件的文件才会被列为孤儿：

1. 文件至少保留 7 天（或 `--min-age` 指定的更长时间）。
2. 全局当前 `channel_accounts.storefront_image_url` 和 `xiaohongshu_product_configs.image_url` 均未引用它。
3. 全局 `audit_logs.before_json` 和 `audit_logs.after_json`（包括历史、软删除审计行）均未引用它。

引用按 `channel-product-images/<tenant>/<account>/<filename>` 的路径识别，不比较 URL 域名。因此更换公网域名不会误把历史图片当作孤儿。审计 JSON 无法完整解析时仍会检查其中的合法图片路径；维护命令不会改写审计记录。

`--apply` 在一次短 PostgreSQL 事务中先按固定顺序锁定 `channel_accounts`、`xiaohongshu_product_configs`、`audit_logs` 的 `SHARE` 锁，再重新读取引用并扫描文件，随后移动文件。事务提交前任何失败都会尽力逆序恢复已经移动的文件；数据库不写入迁移、seed 或清理记录。

## 隔离与恢复

文件会移动到：

```text
uploads/.quarantine/xiaohongshu-image-cleanup/<tenant>/<account>/<filename>
```

该目录不在现有公共图片路由 `channel-products` 下，不会被 HTTP 公共上传路由暴露。命令重复执行是幂等的：已隔离文件不再出现在源扫描中。

需要恢复时，在确认源路径不存在后，将文件移回原路径，并保持原始文件名：

```text
mv uploads/.quarantine/xiaohongshu-image-cleanup/12/34/0123456789abcdef0123456789abcdef.png uploads/channel-products/12/34/0123456789abcdef0123456789abcdef.png
```

Windows 可使用等价的 `Move-Item -LiteralPath`。恢复后如果数据库仍没有引用，下一次显式 `--apply` 会按同样规则再次隔离，这是预期行为。执行前应先运行预览并保存 JSON。移动只改变文件位置，不会减少磁盘占用；当前未启用自动清理任务，需由运维人员显式运行命令。
