#!/usr/bin/env bash
# 每日备份面板数据库，保留 14 天。
#
# 用服务端自己的 backup 子命令而不是拷文件：库开着 WAL，直接拷 vm.db 会丢掉
# 尚未 checkpoint 的事务，拷出来的可能是个损坏的库。
#
# crontab 示例（每天 03:00）：
#   0 3 * * * /opt/vps-monitor/backup.sh >> /var/log/vps-monitor-backup.log 2>&1

set -euo pipefail

cd "$(dirname "$0")"

KEEP_DAYS="${KEEP_DAYS:-14}"
STAMP="$(date +%F)"
TARGET="/data/backup/vm-${STAMP}.db"

# 已经备过就先删掉，否则 VACUUM INTO 会因为目标存在而失败（一天跑两次的情况）
docker compose exec -T server /app/server backup "${TARGET}" 2>/dev/null || {
	rm -f "./data/backup/vm-${STAMP}.db"
	docker compose exec -T server /app/server backup "${TARGET}"
}

echo "$(date -Is) 备份完成: ./data/backup/vm-${STAMP}.db"

# 清理过期备份。用 find 而不是按文件名算日期，免得跨月跨年出岔子。
find ./data/backup -name 'vm-*.db' -type f -mtime "+${KEEP_DAYS}" -print -delete
