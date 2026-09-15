#!/bin/bash
set -euo pipefail
mkdir -p /run/openrc /etc/sing-box /etc/systemd/system /usr/local/bin
touch /run/openrc/softlevel
# OpenRC's dependencies are bypassed in this single-service container only.
export RC_SVCNAME=vps-agent
cat > /usr/local/bin/curl <<'EOF'
#!/bin/bash
set -e
target="${@: -1}"
for arg in "$@"; do
 case "$arg" in
   */vps-agent-linux-amd64.sha256) sha256sum /artifacts/vps-agent-linux-amd64 > "$target"; exit 0 ;;
   */vps-agent-linux-amd64) cp /artifacts/vps-agent-linux-amd64 "$target"; exit 0 ;;
 esac
done
exit 1
EOF
chmod 755 /usr/local/bin/curl
cat > /etc/init.d/net <<'EOF'
#!/sbin/openrc-run
start() { return 0; }
EOF
chmod 755 /etc/init.d/net
rc-service net start
printf 'foreign binary' > /usr/local/bin/sing-box
printf 'foreign config' > /etc/sing-box/config.json
printf 'foreign service' > /etc/systemd/system/sing-box.service
sha256sum /usr/local/bin/sing-box /etc/sing-box/config.json /etc/systemd/system/sing-box.service > /tmp/protected.sha256
bash /scripts/install.sh --server ws://127.0.0.1:1/api/agent/ws --token isolated-test-token
rc-service vps-agent status
if bash /scripts/uninstall.sh --purge-core; then echo 'unsafe purge accepted'; exit 1; fi
sha256sum -c /tmp/protected.sha256
bash /scripts/uninstall.sh
sha256sum -c /tmp/protected.sha256
test ! -e /usr/local/bin/vps-agent
test ! -e /etc/init.d/vps-agent
echo OPENRC_INSTALL_UNINSTALL_AND_EXTERNAL_PRESERVATION_PASS
