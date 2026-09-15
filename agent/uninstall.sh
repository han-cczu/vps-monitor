#!/usr/bin/env bash
# 卸载 VPS Monitor agent。
#
#   bash uninstall.sh [--purge-core]
#
# 默认删除 agent 自身，保留 sing-box 及其修订/恢复状态，供重新安装后接管。
# --purge-core 仅通过新 Agent 的所有权核验删除它安装的核心文件。
set -euo pipefail

BIN_PATH=/usr/local/bin/vps-agent
CONF_DIR=/etc/vps-agent
STATE_DIR=/var/lib/vps-agent
UNIT_PATH=/etc/systemd/system/vps-agent.service
SERVICE=vps-agent

CORE_BIN=/usr/local/bin/sing-box
CORE_DIR=/etc/sing-box
CORE_UNIT=/etc/systemd/system/sing-box.service

PURGE_CORE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --purge-core) PURGE_CORE=1; shift ;;
    -h|--help)
      echo "用法：uninstall.sh [--purge-core]" >&2
      exit 1
      ;;
    *)
      echo "错误：未知参数 $1" >&2
      exit 1
      ;;
  esac
done

[ "$(id -u)" -eq 0 ] || { echo "错误：请用 root 运行" >&2; exit 1; }

if command -v systemctl >/dev/null 2>&1; then
  echo "==> 停止并禁用 ${SERVICE}"
  systemctl stop "$SERVICE" 2>/dev/null || true
  systemctl disable "$SERVICE" 2>/dev/null || true
elif command -v rc-service >/dev/null 2>&1; then
  rc-service "$SERVICE" stop 2>/dev/null || true
  rc-update del "$SERVICE" default 2>/dev/null || true
fi

if [ "$PURGE_CORE" -eq 1 ]; then
  [ -f "$STATE_DIR/core-owner.json" ] || { echo "拒绝清理核心：缺少所有权记录。外部代理保持不变。" >&2; exit 1; }
  "$BIN_PATH" core purge --config "$CONF_DIR/config.yaml" || { echo "核心所有权核验失败，保留文件。" >&2; exit 1; }
fi

echo "==> 删除服务与文件"
rm -f "$UNIT_PATH"
rm -f /etc/init.d/vps-agent
rm -f "$BIN_PATH" "${BIN_PATH}.bak"
rm -rf "$CONF_DIR"


if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  systemctl reset-failed "$SERVICE" 2>/dev/null || true
fi

echo "==> 已卸载。面板上的节点记录需要另行删除。"
