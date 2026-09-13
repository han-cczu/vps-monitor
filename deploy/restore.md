# 从备份恢复

备份文件在 `./data/backup/vm-YYYY-MM-DD.db`，由 `backup.sh`（或 `server backup`）生成。
它是一份完整、已整理过的 SQLite 库，可以直接当 `vm.db` 用。

## 步骤

```sh
cd /opt/vps-monitor          # 放 docker-compose.yml 的目录

# 1. 停掉服务端。Caddy 可以不停——它会返回 502，但证书续期不受影响。
docker compose stop server

# 2. 把当前的库挪开而不是直接覆盖：万一恢复的不是想要的那份，还能退回来。
mv ./data/vm.db ./data/vm.db.before-restore
rm -f ./data/vm.db-wal ./data/vm.db-shm

# 3. 放上备份
cp ./data/backup/vm-2026-09-13.db ./data/vm.db

# 4. 起来
docker compose start server
docker compose logs -f server
```

日志里出现 `server starting` 就成了。确认无误后再删 `vm.db.before-restore`。

## 注意

- **`-wal` 和 `-shm` 必须一起删。** 留着旧的 WAL 文件，SQLite 会把它往新库上套，
  轻则数据对不上，重则直接打不开。
- **恢复的是配置，不是实时状态。** 节点的在线状态、CPU/内存这些都在内存里，
  agent 重连之后几秒内自己就补齐了。
- **agent token 也在库里。** 恢复到一个较早的备份，如果这期间重置过某台节点的 token，
  那台的 agent 会开始报 401，到面板上重新生成一次即可。
- **`jwt.secret` 不在库里**（在 `./data/jwt.secret`）。只恢复数据库不影响已登录的会话；
  整机重装时这个文件也要一起带过来，否则所有人都得重新登录。

## 只想看看备份里有什么

```sh
sqlite3 ./data/backup/vm-2026-09-13.db '.tables'
sqlite3 ./data/backup/vm-2026-09-13.db 'SELECT id, name, region FROM servers;'
sqlite3 ./data/backup/vm-2026-09-13.db 'PRAGMA integrity_check;'
```

宿主上没装 sqlite3 的话，任何能读 SQLite 的工具都行（Python 自带 `sqlite3` 模块）。

## 忘了管理员密码

不用恢复备份，直接重置：

```sh
docker compose exec -T server /app/server reset-password admin
```

它会打印一个新的随机密码，立即生效，不用重启。
