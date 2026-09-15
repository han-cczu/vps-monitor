#!/usr/bin/env bash
# VPS Monitor agent 安装脚本。
#
#   curl -fsSL https://panel.example.com/install.sh | bash -s -- \
#     --server wss://panel.example.com/api/agent/ws --token TOKEN
#
# 重复执行等于升级 + 重启，已有配置里只有 server / token 会被更新。
set -euo pipefail

BIN_PATH=/usr/local/bin/vps-agent
CONF_DIR=/etc/vps-agent
CONF_PATH="${CONF_DIR}/config.yaml"
UNIT_PATH=/etc/systemd/system/vps-agent.service
SERVICE=vps-agent

SERVER=""
TOKEN=""
BASE=""

die() {
  echo "错误：$*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
用法：install.sh --server wss://面板地址/api/agent/ws --token TOKEN [--base https://面板地址]

  --server  agent 连接的 WebSocket 地址（面板创建节点时给出）
  --token   agent token（同上，只显示一次）
  --base    下载二进制用的 HTTP 地址，默认由 --server 推导
EOF
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2 ;;
    --token)  TOKEN="${2:-}";  shift 2 ;;
    --base)   BASE="${2:-}";   shift 2 ;;
    -h|--help) usage ;;
    *) die "未知参数 $1（--help 看用法）" ;;
  esac
done

[ -n "$SERVER" ] || usage
[ -n "$TOKEN" ] || usage

[ "$(id -u)" -eq 0 ] || die "请用 root 运行（sudo bash ...）"
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  INIT=systemd
elif command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then
  INIT=openrc
else
  die "需要 systemd 或 OpenRC；其它系统可手动运行探针"
fi

# 下载器：curl 优先，退回 wget
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsS --connect-timeout 10 --max-time 60 --max-filesize 67108864 "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { (ulimit -f 65536; wget -q --timeout=60 --tries=1 --max-redirect=0 -O "$2" "$1"); }
else
  die "需要 curl 或 wget"
fi

# 架构
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的架构 $(uname -m)（只提供 amd64 与 arm64）" ;;
esac

# --base 缺省由 --server 推导：wss:// → https://、ws:// → http://，去掉路径
if [ -z "$BASE" ]; then
  BASE="$SERVER"
  case "$BASE" in
    wss://*) BASE="https://${BASE#wss://}" ;;
    ws://*)  BASE="http://${BASE#ws://}" ;;
    *) die "--server 需要以 ws:// 或 wss:// 开头，当前为 $SERVER" ;;
  esac
  # 只保留协议 + host，去掉路径
  proto="${BASE%%://*}"
  hostpath="${BASE#*://}"
  BASE="${proto}://${hostpath%%/*}"
fi

echo "==> 面板地址 ${BASE}，架构 ${ARCH}"

# 1. 下载二进制到临时文件再原子替换，避免把正在运行的文件写坏
TMP_BIN="$(mktemp "${BIN_PATH}.XXXXXX")"
TMP_SHA="${TMP_BIN}.sha256"
trap 'rm -f "$TMP_BIN" "$TMP_SHA"' EXIT
echo "==> 下载 ${BASE}/agent/vps-agent-linux-${ARCH}"
fetch "${BASE}/agent/vps-agent-linux-${ARCH}" "$TMP_BIN" || die "下载失败，检查面板地址是否可访问"
[ -s "$TMP_BIN" ] || die "下载到的文件是空的"
[ "$(wc -c < "$TMP_BIN")" -le 67108864 ] || die "二进制超过64MiB上限"
command -v sha256sum >/dev/null 2>&1 || die "需要sha256sum校验下载文件"
fetch "${BASE}/agent/vps-agent-linux-${ARCH}.sha256" "$TMP_SHA" || die "下载校验摘要失败"
EXPECTED_SHA="$(awk 'NR==1 {print $1}' "$TMP_SHA")"
[[ "$EXPECTED_SHA" =~ ^[0-9a-f]{64}$ ]] || die "校验摘要格式错误"
ACTUAL_SHA="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
[ "$ACTUAL_SHA" = "$EXPECTED_SHA" ] || die "SHA256不匹配，保留现有Agent"
rm -f "$TMP_SHA"
chmod 755 "$TMP_BIN"
"$TMP_BIN" --version >/dev/null 2>&1 || die "下载到的二进制跑不起来（架构不匹配？）"
mv -f "$TMP_BIN" "$BIN_PATH"
trap - EXIT
echo "==> 已安装 ${BIN_PATH}（版本 $("$BIN_PATH" --version)）"

# 2. 配置：已存在就只更新 server 与 token，保留用户改过的其它项
mkdir -p "$CONF_DIR"
chmod 700 "$CONF_DIR"
if [ -f "$CONF_PATH" ]; then
  echo "==> 更新已有配置里的 server 与 token"
  tmp_conf="$(mktemp)"
  SERVER="$SERVER" TOKEN="$TOKEN" awk '
    /^server:/ { print "server: " ENVIRON["SERVER"]; seen_server=1; next }
    /^token:/  { print "token: \"" ENVIRON["TOKEN"] "\""; seen_token=1; next }
    { print }
    END {
      if (!seen_server) print "server: " ENVIRON["SERVER"]
      if (!seen_token)  print "token: \"" ENVIRON["TOKEN"] "\""
    }
  ' "$CONF_PATH" > "$tmp_conf"
  cat "$tmp_conf" > "$CONF_PATH"
  rm -f "$tmp_conf"
else
  echo "==> 写入 ${CONF_PATH}"
  cat > "$CONF_PATH" <<EOF
server: ${SERVER}
token: "${TOKEN}"
report_interval: 1
interfaces:
  exclude: ["lo", "docker*", "veth*", "br-*", "tun*", "tap*", "tailscale*", "wg*"]
disk_mounts: ["/"]
log_level: info
core:
  stats_address: "127.0.0.1:10085"
EOF
fi
chmod 600 "$CONF_PATH"

# 3. systemd unit
if [ "$INIT" = openrc ]; then
  cat > /etc/init.d/vps-agent <<'EOF'
#!/sbin/openrc-run
description="VPS Monitor Agent"
command="/usr/local/bin/vps-agent"
command_args="--config /etc/vps-agent/config.yaml"
command_background="yes"
pidfile="/run/vps-agent.pid"
output_log="/var/log/vps-agent.log"
error_log="/var/log/vps-agent.log"
depend() { need net; }
EOF
  chmod 755 /etc/init.d/vps-agent
  rc-update add "$SERVICE" default
  rc-service "$SERVICE" restart
  echo "==> 完成。日志：/var/log/vps-agent.log（外部核心仅观测）"
  exit 0
fi
echo "==> 写入 ${UNIT_PATH}"
cat > "$UNIT_PATH" <<EOF
[Unit]
Description=VPS Monitor Agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${BIN_PATH} --config ${CONF_PATH}
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1 || true
systemctl restart "$SERVICE"

echo
systemctl --no-pager --lines=0 status "$SERVICE" | head -n 5 || true
echo
journalctl -u "$SERVICE" -n 5 --no-pager 2>/dev/null || true
echo
echo "==> 完成。日志：journalctl -u ${SERVICE} -f"
