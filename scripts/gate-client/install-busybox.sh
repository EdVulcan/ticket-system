#!/bin/sh
set -eu

usage() {
    echo "用法: $0 --server-url https://your-host [--package-dir ./gate-client-armv7]" >&2
    exit 2
}

SERVER_URL=""
PACKAGE_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
while [ "$#" -gt 0 ]; do
    case "$1" in
        --server-url)
            [ "$#" -ge 2 ] || usage
            SERVER_URL="$2"
            shift 2
            ;;
        --package-dir)
            [ "$#" -ge 2 ] || usage
            PACKAGE_DIR="$2"
            shift 2
            ;;
        -h|--help) usage ;;
        *) usage ;;
    esac
done

[ "$(id -u)" -eq 0 ] || { echo "请使用 root 执行安装器" >&2; exit 1; }
[ -n "$SERVER_URL" ] || { echo "必须提供 --server-url；绑定码不会放在命令行" >&2; exit 1; }
case "$(uname -m)" in
    armv7l|armv7*) : ;;
    *) echo "当前 ARMv7 发布包不适用于 $(uname -m)" >&2; exit 1 ;;
esac

for required in gate-client gate-provision ticket-gate-busybox.init \
    ticket-gate-busybox-run.sh ticket-gate-busybox-cli.sh; do
    [ -f "$PACKAGE_DIR/$required" ] || { echo "缺少 $PACKAGE_DIR/$required" >&2; exit 1; }
done

command -v adduser >/dev/null 2>&1 || { echo "系统缺少 adduser，拒绝以 root 长期运行闸机客户端" >&2; exit 1; }
command -v start-stop-daemon >/dev/null 2>&1 || { echo "系统缺少 start-stop-daemon" >&2; exit 1; }

APP_USER="ticket-gate"
APP_HOME="/var/lib/ticket-gate"
ETC_DIR="/etc/ticket-gate"
BIN_DIR="/usr/local/lib/ticket-gate"
ENV_FILE="$ETC_DIR/gate-client.env"
INIT_FILE="/etc/init.d/S98-ticket-gate"
PID_FILE="/var/run/ticket-gate.pid"

if ! id -u "$APP_USER" >/dev/null 2>&1; then
    # BusyBox versions differ in the supported adduser flags. A failed first
    # attempt may still have created the account, so re-check before trying a
    # smaller fallback rather than treating the second error as authoritative.
    adduser -S -D -H -s /bin/false "$APP_USER" >/dev/null 2>&1 || true
fi
if ! id -u "$APP_USER" >/dev/null 2>&1; then
    adduser -S -D "$APP_USER" >/dev/null 2>&1 || true
fi
if ! id -u "$APP_USER" >/dev/null 2>&1; then
    echo "无法创建受限用户 $APP_USER，拒绝安装" >&2
    exit 1
fi

APP_UID="$(id -u "$APP_USER")"
APP_GID="$(id -g "$APP_USER")"
APP_OWNER="$APP_UID:$APP_GID"

mkdir -p "$APP_HOME" "$ETC_DIR" "$BIN_DIR" /usr/local/bin
chmod 0750 "$APP_HOME" "$ETC_DIR"
chown "$APP_OWNER" "$APP_HOME"
chown root:"$APP_GID" "$ETC_DIR"

copy_executable() {
    source_path="$1"
    target_path="$2"
    temporary_path="$target_path.new.$$"
    cp "$source_path" "$temporary_path"
    chmod 0755 "$temporary_path"
    chown root:root "$temporary_path"
    mv -f "$temporary_path" "$target_path"
}

copy_executable "$PACKAGE_DIR/gate-client" "$BIN_DIR/gate-client"
copy_executable "$PACKAGE_DIR/gate-provision" "$BIN_DIR/gate-provision"
copy_executable "$PACKAGE_DIR/ticket-gate-busybox-run.sh" "$BIN_DIR/ticket-gate-busybox-run.sh"
copy_executable "$PACKAGE_DIR/ticket-gate-busybox.init" "$INIT_FILE"
copy_executable "$PACKAGE_DIR/ticket-gate-busybox-cli.sh" /usr/local/bin/ticket-gate

PROVISIONING_STATE="$APP_HOME/.provisioning.json"
if [ ! -f "$ENV_FILE" ] || [ -f "$PROVISIONING_STATE" ]; then
    if [ -f "$ENV_FILE" ] && [ -f "$PROVISIONING_STATE" ]; then
        echo "检测到上次安装尚未完成确认，将恢复同一安装绑定。请再次输入原绑定码。" >&2
    else
        echo "即将启动一次性安装绑定。请在提示处输入管理端生成的绑定码。" >&2
    fi
    "$BIN_DIR/gate-provision" \
        --server-url "$SERVER_URL" \
        --config "$ENV_FILE" \
        --state-dir "$APP_HOME"
else
    echo "检测到已有 $ENV_FILE，跳过重新绑定。" >&2
fi

[ -f "$ENV_FILE" ] || { echo "安装绑定后没有生成配置文件：$ENV_FILE" >&2; exit 1; }
chown root:"$APP_GID" "$ENV_FILE"
chmod 0640 "$ENV_FILE"
chown "$APP_OWNER" "$APP_HOME"
chmod 0750 "$APP_HOME"
rm -f "$PID_FILE"

"$INIT_FILE" start
echo "ARMv7 BusyBox gate-client 已安装并启动。"
echo "执行 ticket-gate doctor 检查；未配置真实三辊闸驱动时会保持 unknown/fail-closed。"
