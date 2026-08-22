#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "用法: sudo $0 --server-url https://your-host [--package-dir ./dist] [--rebind]" >&2
  exit 2
}

SERVER_URL=""
PACKAGE_DIR="$(cd "$(dirname "$0")/../../dist" 2>/dev/null && pwd || true)"
REBIND=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server-url) [ "$#" -ge 2 ] || usage; SERVER_URL="$2"; shift 2 ;;
    --package-dir) [ "$#" -ge 2 ] || usage; PACKAGE_DIR="$2"; shift 2 ;;
    --rebind) REBIND=1; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done

[ "$(id -u)" -eq 0 ] || { echo "请使用 root 执行安装器" >&2; exit 1; }
[ -n "$SERVER_URL" ] || { echo "必须提供 --server-url；绑定码不会放在命令行" >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) ;;
  *) echo "当前发布包仅支持 Linux amd64（检测到 $(uname -m)）；请构建对应架构后再安装" >&2; exit 1 ;;
esac
# The release is often copied from Windows or unpacked from a zip, so the
# source files may not carry a Unix executable bit. `install -m 0755` below
# applies the authoritative mode after checking that both binaries exist.
[ -f "$PACKAGE_DIR/gate-client" ] || { echo "缺少 $PACKAGE_DIR/gate-client" >&2; exit 1; }
[ -f "$PACKAGE_DIR/gate-provision" ] || { echo "缺少 $PACKAGE_DIR/gate-provision" >&2; exit 1; }

APP_USER="ticket-gate"
APP_GROUP="ticket-gate"
APP_HOME="/var/lib/ticket-gate"
ETC_DIR="/etc/ticket-gate"
BIN_DIR="/usr/local/lib/ticket-gate"
ENV_FILE="$ETC_DIR/gate-client.env"

if ! getent group "$APP_GROUP" >/dev/null 2>&1; then
  groupadd --system "$APP_GROUP"
fi
if ! id -u "$APP_USER" >/dev/null 2>&1; then
  useradd --system --gid "$APP_GROUP" --home-dir "$APP_HOME" --shell /usr/sbin/nologin "$APP_USER"
fi

install -d -o "$APP_USER" -g "$APP_GROUP" -m 0750 "$APP_HOME"
install -d -o root -g "$APP_GROUP" -m 0750 "$ETC_DIR"
install -d -o root -g root -m 0755 "$BIN_DIR"
install -o root -g root -m 0755 "$PACKAGE_DIR/gate-client" "$BIN_DIR/gate-client"
install -o root -g root -m 0755 "$PACKAGE_DIR/gate-provision" "$BIN_DIR/gate-provision"

PROVISIONING_STATE="$APP_HOME/.provisioning.json"
if [ "$REBIND" -eq 1 ] && [ -f "$ENV_FILE" ]; then
  systemctl stop ticket-gate.service >/dev/null 2>&1 || true
  backup_path="$ETC_DIR/gate-client.env.backup.$(date -u +%Y%m%d%H%M%S).$$"
  mv -- "$ENV_FILE" "$backup_path"
  chown root:root "$backup_path"
  chmod 0600 "$backup_path"
  echo "旧配置已备份到 $backup_path；将开始新的安装绑定。" >&2
fi
if [ ! -f "$ENV_FILE" ] || [ -f "$PROVISIONING_STATE" ]; then
  if [ -f "$ENV_FILE" ] && [ -f "$PROVISIONING_STATE" ]; then
    echo "检测到上次安装尚未完成确认，将恢复同一安装绑定。请再次输入原绑定码。" >&2
  else
    echo "即将启动一次性安装绑定。请在提示处粘贴管理端生成的绑定码。" >&2
  fi
  "$BIN_DIR/gate-provision" \
    --server-url "$SERVER_URL" \
    --config "$ENV_FILE" \
    --state-dir "$APP_HOME"
else
  echo "检测到已有 $ENV_FILE，跳过重新绑定；如需轮换请在管理端生成新租约并人工备份后移除旧配置。" >&2
fi

chown root:"$APP_GROUP" "$ENV_FILE"
chmod 0640 "$ENV_FILE"
chown -R "$APP_USER":"$APP_GROUP" "$APP_HOME"
chmod 0750 "$APP_HOME"

install -o root -g root -m 0755 "$(dirname "$0")/ticket-gate-cli.sh" /usr/local/bin/ticket-gate
install -d -o root -g root -m 0755 /etc/systemd/system
install -o root -g root -m 0644 "$(dirname "$0")/ticket-gate.service" /etc/systemd/system/ticket-gate.service
systemctl daemon-reload
systemctl enable --now ticket-gate.service

echo "gate-client 已安装并启动。" >&2
echo "执行 ticket-gate doctor 检查；未配置 GATE_DRIVER_URL 时会保持 fail-closed。" >&2
