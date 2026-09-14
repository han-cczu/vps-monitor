#!/usr/bin/env bash
# 卸载 VPS Monitor agent。
#
#   bash uninstall.sh [--purge-core]
#
# 默认删除 agent 自身，保留 sing-box 及其修订/恢复状态，供重新安装后接管。
# --purge-core 连 agent 装的 sing-box 及状态一起删。
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
fi

echo "==> 删除服务与文件"
rm -f "$UNIT_PATH"
rm -f "$BIN_PATH"
rm -rf "$CONF_DIR"

if [ "$PURGE_CORE" -eq 1 ]; then
  echo "==> 一并删除 sing-box"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl stop sing-box 2>/dev/null || true
    systemctl disable sing-box 2>/dev/null || true
  fi
  rm -f "$CORE_UNIT"
  rm -f "$CORE_BIN"
  rm -rf "$CORE_DIR"
  rm -rf /var/log/sing-box
  rm -rf "$STATE_DIR"
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  systemctl reset-failed "$SERVICE" 2>/dev/null || true
fi

echo "==> 已卸载。面板上的节点记录需要另行删除。"
