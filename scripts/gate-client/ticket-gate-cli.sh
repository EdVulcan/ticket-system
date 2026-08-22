#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="/etc/ticket-gate/gate-client.env"
ACTION="${1:-status}"

if [ ! -r "$ENV_FILE" ]; then
  echo "配置文件不存在：$ENV_FILE" >&2
  exit 1
fi
get_env_value() {
  local wanted="$1"
  local name value
  while IFS='=' read -r name value; do
    [ "$name" = "$wanted" ] || continue
    value="${value%$'\r'}"
    printf '%s' "$value"
    return 0
  done < "$ENV_FILE"
  return 1
}

# The file is root:ticket-gate 0640; this helper is intended for local
# operators who are already authorized to inspect the gate runtime. Parse
# only the expected keys instead of sourcing the file as shell code.
GATE_SCAN_TOKEN="$(get_env_value GATE_SCAN_TOKEN || true)"
GATE_SCAN_LISTEN="$(get_env_value GATE_SCAN_LISTEN || true)"
GATE_STATE_FILE="$(get_env_value GATE_STATE_FILE || true)"
GATE_DRIVER_URL="$(get_env_value GATE_DRIVER_URL || true)"
STATE_FILE="${GATE_STATE_FILE:-/var/lib/ticket-gate/state.json}"

case "$ACTION" in
  status)
    exec curl --fail-with-body --silent --show-error --max-time 5 \
      -H "Authorization: Bearer $GATE_SCAN_TOKEN" \
      "http://${GATE_SCAN_LISTEN:-127.0.0.1:19300}/status"
    ;;
  doctor)
    failed=0
    [ "$(stat -c '%a' "$ENV_FILE")" = "640" ] || { echo "FAIL: 配置权限应为 0640"; failed=1; }
    [ "$(systemctl is-enabled ticket-gate.service 2>/dev/null || true)" = "enabled" ] || { echo "WARN: systemd 未设置开机启动"; failed=1; }
    if [ -n "${GATE_DRIVER_URL:-}" ]; then
      echo "OK: 已配置驱动适配器地址"
    else
      echo "WARN: 未配置真实三辊闸驱动；扫码将保持 unknown/fail-closed"
    fi
    if curl --fail --silent --max-time 5 -H "Authorization: Bearer $GATE_SCAN_TOKEN" "http://${GATE_SCAN_LISTEN:-127.0.0.1:19300}/status" >/dev/null; then
      echo "OK: gate-client 本地状态接口可用"
    else
      echo "FAIL: gate-client 本地状态接口不可用"
      failed=1
    fi
    [ -e "$STATE_FILE" ] && echo "OK: 恢复状态文件存在" || echo "WARN: 尚无待恢复状态文件（首次启动正常）"
    exit "$failed"
    ;;
  logs)
    exec journalctl -u ticket-gate.service --no-pager -n "${2:-100}"
    ;;
  restart)
    exec systemctl restart ticket-gate.service
    ;;
  *)
    echo "用法: ticket-gate status|doctor|logs [行数]|restart" >&2
    exit 2
    ;;
esac
