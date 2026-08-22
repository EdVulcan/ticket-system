#!/bin/sh
set -u

ENV_FILE="/etc/ticket-gate/gate-client.env"
INIT_FILE="/etc/init.d/S98-ticket-gate"
LOG_FILE="/var/lib/ticket-gate/gate-client.log"
ACTION="${1:-status}"

if [ ! -r "$ENV_FILE" ]; then
    echo "配置文件不存在：$ENV_FILE" >&2
    exit 1
fi

get_env_value() {
    wanted="$1"
    while IFS='=' read -r name value; do
        [ "$name" = "$wanted" ] || continue
        printf '%s' "$value"
        return 0
    done < "$ENV_FILE"
    return 1
}

GATE_SCAN_TOKEN="$(get_env_value GATE_SCAN_TOKEN || true)"
GATE_SCAN_LISTEN="$(get_env_value GATE_SCAN_LISTEN || true)"
GATE_DRIVER_URL="$(get_env_value GATE_DRIVER_URL || true)"
STATE_FILE="$(get_env_value GATE_STATE_FILE || true)"
GATE_SCAN_LISTEN="${GATE_SCAN_LISTEN:-127.0.0.1:19300}"
STATE_FILE="${STATE_FILE:-/var/lib/ticket-gate/state.json}"

local_status() {
    wget -q -O - -T 5 --header "Authorization: Bearer $GATE_SCAN_TOKEN" \
        "http://$GATE_SCAN_LISTEN/status"
}

case "$ACTION" in
    status)
        local_status
        ;;
    doctor)
        failed=0
        [ -x "$INIT_FILE" ] || { echo "FAIL: BusyBox 启动脚本不存在"; failed=1; }
        if "$INIT_FILE" status >/dev/null 2>&1; then
            echo "OK: gate-client 进程运行中"
        else
            echo "FAIL: gate-client 未运行"
            failed=1
        fi
        if local_status >/dev/null 2>&1; then
            echo "OK: 本地状态接口可用"
        else
            echo "FAIL: 本地状态接口不可用"
            failed=1
        fi
        [ -e "$STATE_FILE" ] && echo "OK: 恢复状态文件存在" || echo "WARN: 尚无待恢复状态文件（首次启动正常）"
        [ -n "$GATE_DRIVER_URL" ] && echo "OK: 已配置驱动适配器地址" || echo "WARN: 未配置真实三辊闸驱动；扫码将保持 unknown/fail-closed"
        exit "$failed"
        ;;
    logs)
        lines="${2:-100}"
        tail -n "$lines" "$LOG_FILE"
        ;;
    restart)
        "$INIT_FILE" restart
        ;;
    stop)
        "$INIT_FILE" stop
        ;;
    start)
        "$INIT_FILE" start
        ;;
    *)
        echo "用法: ticket-gate status|doctor|logs [行数]|start|stop|restart" >&2
        exit 2
        ;;
esac
