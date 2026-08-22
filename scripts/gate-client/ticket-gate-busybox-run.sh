#!/bin/sh
set -eu

ENV_FILE="/etc/ticket-gate/gate-client.env"
BIN="/usr/local/lib/ticket-gate/gate-client"
LOG_FILE="/var/lib/ticket-gate/gate-client.log"

[ -r "$ENV_FILE" ] || { echo "配置文件不存在：$ENV_FILE" >&2; exit 1; }
[ -x "$BIN" ] || { echo "gate-client 不可执行：$BIN" >&2; exit 1; }

# The provisioning binary writes a fixed set of KEY=value lines. Parse only
# those keys instead of sourcing the file as shell code; this keeps a malformed
# or tampered local file from becoming an arbitrary command execution path.
get_env_value() {
    wanted="$1"
    while IFS='=' read -r name value; do
        [ "$name" = "$wanted" ] || continue
        printf '%s' "$value"
        return 0
    done < "$ENV_FILE"
    return 1
}

for key in \
    GATE_SERVER_URL \
    GATE_SYSTEM_CODE \
    GATE_SERIAL_NUMBER \
    GATE_DEVICE_KEY \
    GATE_MAINTENANCE_SECRET \
    GATE_MAINTENANCE_URL \
    GATE_DRIVER_URL \
    GATE_SCAN_TOKEN \
    GATE_STATE_FILE \
    GATE_SCAN_LISTEN; do
    value="$(get_env_value "$key" || true)"
    export "$key=$value"
done

umask 027
mkdir -p "$(dirname "$LOG_FILE")"
exec "$BIN" >>"$LOG_FILE" 2>&1
