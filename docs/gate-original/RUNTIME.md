# 原厂运行时、启动和共存注释

## 1. 设备事实

- ARMv7 / Freescale i.MX6。
- BusyBox init；没有 Bash、systemd。
- 原厂主程序：`/usr/sendinfo/ticket -qws`。
- 扫码器配置：`/dev/ttyUSB2`，由原厂程序独占。
- 原厂看门狗：`/usr/sendinfo/watchdog 5 2 0`。
- `S99-fluidlauncher` 在有效逻辑前无条件 `exit 0`，不能当作实际桌面自启动。

## 2. gate-client 离线线索

现场曾记录受限用户读取 `/dev/urandom` 被拒绝，Go heartbeat goroutine 因 `crypto/rand` fatal 退出；重启 `S98-ticket-gate` 后恢复在线。S98 会在降权前执行 `chmod 0666 /dev/urandom`，但它不是持续崩溃监督器，且设备重启后节点权限可能再次恢复为 `root:root 0660`。

`ticket-gate status=ok` 只证明本机 `127.0.0.1:19300` 可访问，不能证明云端心跳已被服务端接受。

## 3. 原厂共存风险

`start_a9.sh` 用 `ps | grep "ticket"` 判断原厂程序。`ticket-gate` 的命令行可能命中该条件，导致原厂 `/usr/sendinfo/ticket` 退出后不被拉起。原厂脚本另外会因陈旧 `/tmp/heartbeat`、固定时间窗口或配置更新执行 `reboot`；watchdog 也可能触发整机复位。

因此排查离线时必须联合查看客户端日志、原厂日志、进程列表、watchdog、`/tmp/heartbeat` 和服务端最近心跳，不能只重启某一个进程后下结论。

## 4. 只读现场采集

```sh
date; date -u
ps | grep -E '[t]icket|[w]atchdog|[g]ate-client'
ls -l /dev/urandom /var/run/ticket-gate.pid /tmp/heartbeat 2>&1
/etc/init.d/S98-ticket-gate status 2>&1
tail -n 200 /var/lib/ticket-gate/gate-client.log 2>&1
tail -n 200 /usr/sendinfo/restart.log /usr/sendinfo/err.log 2>&1
```

以上命令只读，不应在同一轮排查中执行 `restart`、`kill`、`chmod`、写 `/tmp/heartbeat`、读取 `/dev/ttyUSB2` 或发送 CAN。

详细证据：`../../artifacts/gate-original/runtime-evidence-audit-20260823.md`。
