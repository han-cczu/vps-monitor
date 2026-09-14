# 运维手册

> 本文件随代码走：每个步骤新增的部署、备份、排障动作都记到这里。

## 1. 本地开发

前置：Go 1.26、Node 24、npm 11。

```sh
# 一次性：装前端依赖
cd web && npm i

# 两个终端
make dev-server   # Go 服务端，监听 :9000
make dev-web      # Vite dev server，监听 :8080，/api 与 /api/ws 代理到 :9000
```

Windows 上如果没装 make，直接跑等价命令：

```sh
go run ./server/cmd/server     # 等价于 make dev-server
cd web && npm run dev          # 等价于 make dev-web
```

npm 全局缓存目录若不可写（典型报错 `EPERM ... node_cache`），用仓库内缓存绕开：

```sh
npm i --cache ../.npm
```

打开 http://localhost:8080 。健康检查：`curl http://localhost:9000/api/health`。

### 环境变量

| 变量 | 默认 | 说明 | 引入步骤 |
|---|---|---|---|
| `VM_LISTEN` | `:9000` | 服务端监听地址 | 01 |
| `VM_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` | 01 |
| `VM_DATA_DIR` | `./data` | 数据目录：`vm.db`（SQLite）、`jwt.secret`；后续步骤还会放 `corefiles/`、`agent/`、`backup/`。目录以 0700 创建 | 02 |
| `VM_PUBLIC_URL` | 空 | 面板对外地址，如 `https://panel.example.com`（拼一键安装命令等用）；为空时用请求的 Host | 02 |
| `VM_JWT_SECRET` | 空 | JWT 签名密钥，至少 32 字节；为空则首次启动随机生成并写入 `{VM_DATA_DIR}/jwt.secret`（权限 0600），之后每次启动读它 | 02 |
| `VM_TZ` | `Asia/Shanghai` | 业务时区，账期与"每日 00:00"类任务按它算；二进制内嵌了 tzdata，精简镜像里也能用 | 02 |
| `VM_TRUSTED_PROXIES` | 空 | 前面有几层可信反向代理。**留空 = 直连**，客户端 IP 只认 TCP 连接地址，所有代理头一概不信。详见下一节 | 02 |
| `VM_AGENT_DIST` | 空 | 镜像自带的 agent 产物目录（镜像里是 `/app/agent-dist`）。启动时同步到 `{VM_DATA_DIR}/agent/`，于是「升级镜像 = 升级节点能下载到的 agent」。本地开发不设它，同步整个跳过 | 07 |

### VM_TRUSTED_PROXIES：让限速和审计拿到真实 IP

登录限速按客户端 IP 计数，审计也记这个 IP。IP 取错了后果很直接：取成代理的 IP，所有人共用一个限速桶；采信了客户端能伪造的请求头，攻击者每次换一个头就能无限试密码。

取值三种写法：

| 写法 | 含义 | 什么时候用 |
|---|---|---|
| 留空（默认） | 客户端 IP = TCP 连接地址，忽略一切代理头 | 本地开发；服务端直接对外 |
| `1`、`2`… | 前面有固定 N 层可信代理，取 `X-Forwarded-For` 从右往左第 N 个 | 生产：Caddy 在同机反代就写 `1`；`Cloudflare → Caddy` 写 `2` |
| `10.0.0.0/8,172.18.0.0/16` | 从右往左跳过落在这些网段里的地址，第一个不在其中的就是客户端 | 代理 IP 固定或有官方网段表（Cloudflare / AWS 等）时，比数层数更稳 |

服务端启动日志里会打出实际生效的模式，上线后核对一遍：

```json
{"msg":"server starting","trusted_proxies":"hops:1"}
{"msg":"server starting","trusted_proxies":"direct (客户端 IP 取 TCP 连接地址，不采信代理头)"}
```

**上线必做一次验证**：从一台已知公网 IP 的机器故意输错一次密码，然后查审计表：

```sh
sqlite3 data/vm.db "SELECT ts, actor, action, ip FROM audit_log ORDER BY id DESC LIMIT 5;"
```

登录成功那行的 `ip` 应该正好是你那台机器的公网 IP。如果显示的是代理的内网地址（例如 `127.0.0.1`、`172.x`），说明层数配少了，客户端可以伪造 IP，要马上改；如果为空或仍是连接地址，说明层数配多了，不会泄露什么，但限速会退化成按代理 IP 计数。

注意这只解决"谁是客户端"，不解决"谁能连上来"。生产环境仍要用防火墙/安全组保证只有 Caddy 能访问 9000 端口。

### 首次启动与初始密码

第一次启动时 `users` 表为空，服务端自动创建管理员 `admin`，随机 16 位密码**只在日志里打印这一次**：

```json
{"level":"WARN","msg":"initial admin password (printed only once)","username":"admin","password":"xxxxxxxxxxxxxxxx"}
```

用它登录后马上到「设置 → 账号」改密码。之后再启动不会重复创建、也不会再打印。

启动完成后 `{VM_DATA_DIR}/` 下会有 `vm.db`（以及 WAL 模式的 `vm.db-wal`、`vm.db-shm`）和 `jwt.secret`。目录本身是 0700、`jwt.secret` 是 0600：里面有密码哈希和签名密钥，不能让同机其他用户读到。

### 节点与 agent 分发（步骤 03）

面板里「节点 → 新增节点」保存后会弹出 **agent token 与一键安装命令，只显示这一次**。命令形如：

```sh
curl -fsSL https://panel.example.com/install.sh | bash -s -- --server wss://panel.example.com/api/agent/ws --token xxxx
```

命令里的面板地址取 `VM_PUBLIC_URL`。**放在 Caddy 后面就必须配它**，否则拼出来的是请求头里的 Host（可能是内网地址或端口）。
本地开发不配也行：会拼成 `http://localhost:9000`，`--server` 相应是 `ws://`。

`/install.sh` 与 `/agent/{file}` 从 `{VM_DATA_DIR}/agent/` 下发，**不需要登录**（agent 装机时还没有任何凭据），
只认四个文件名：`install.sh`、`uninstall.sh`、`vps-agent-linux-amd64`、`vps-agent-linux-arm64`。
其余名字（含任何目录穿越写法）一律 404，所以往这个目录里放别的东西不会被下载到。

脚本与二进制本身是步骤 04 的产出；步骤 07 的镜像会把 CI 产物放进去。在那之前本地这样放：

```sh
make build-agent                      # 产出 dist/agent/vps-agent-linux-{amd64,arm64}
mkdir -p data/agent
cp dist/agent/* agent/install.sh data/agent/
```

没放之前访问 `/install.sh` 会返回纯文本 404（不是 500，也不是前端页面），属正常。

### agent（步骤 04）

装 agent 前先把产物放进面板的下载目录（见上一节）：

```sh
make build-agent                       # dist/agent/：两个架构的二进制 + install.sh + uninstall.sh
cp dist/agent/* {VM_DATA_DIR}/agent/
```

然后在节点上执行面板给的一键命令（`--base` 不写就按 `--server` 推导）：

```sh
curl -fsSL https://panel.example.com/install.sh | bash -s -- \
  --server wss://panel.example.com/api/agent/ws --token TOKEN
```

脚本做的事：认架构（x86_64 / aarch64，其余退出）→ 下载到临时文件、跑一次 `--version` 验证、原子替换
`/usr/local/bin/vps-agent` → 写 `/etc/vps-agent/config.yaml`（0600）与 systemd unit → `enable --now` →
打印状态与最近 5 行日志。**重复执行等于升级 + 重启**，已有配置只有 `server` 与 `token` 会被改写，
其余项（上报间隔、网卡过滤、挂载点）保留。

配置文件：

```yaml
server: wss://panel.example.com/api/agent/ws
token: "..."
report_interval: 1          # 秒，1–60；服务端的 config 消息可以覆盖
interfaces:
  exclude: ["lo", "docker*", "veth*", "br-*", "tun*", "tap*", "tailscale*", "wg*"]
disk_mounts: ["/"]          # 多个挂载点会求和，同一设备只算一次
log_level: info
```

常用命令：

```sh
systemctl status vps-agent
journalctl -u vps-agent -f           # 日志是 JSON，一行一条
vps-agent --once                     # 采集一次打印 JSON 就退出，不联网，用来对数
vps-agent --version
bash uninstall.sh                    # 卸载 agent，保留 sing-box 与核心修订/恢复状态
bash uninstall.sh --purge-core       # 一并删除 sing-box 服务、二进制、配置、日志和状态
```

## 2. 构建

```sh
make build-web      # 前端构建产物拷到 server/web/dist
make build-server   # 内嵌前端，产出 dist/server/vps-server
make build-agent    # 产出 dist/agent/vps-agent-linux-{amd64,arm64}
```

发布用的镜像（构建上下文是仓库根目录，不是 deploy/）：

```sh
docker build -f deploy/Dockerfile -t vps-monitor-server:dev --build-arg VERSION=v0.1.0 .
```

镜像里同时带上两个架构的 agent 二进制与安装脚本，服务端启动时同步进数据卷。
正式发版由 `.github/workflows/release.yml` 在推 `v*` tag 时自动构建并推到 GHCR。

## 3. 部署

面板以两个容器跑：`server`（Go，内嵌前端）和 `caddy`（反代 + 自动 HTTPS）。
只有 Caddy 对外开 80/443，`server` 只在 compose 的内部网络里，宿主上访问不到 9000。

### 首次安装

前提：一台能装 Docker 的机器、一个已经把 A 记录解析过来的域名、80/443 放行。

```sh
# 1. 装 Docker（官方脚本，Debian / Ubuntu 都行）
curl -fsSL https://get.docker.com | sh

# 2. 把 deploy/ 放到机器上
mkdir -p /opt/vps-monitor && cd /opt/vps-monitor
# 从仓库拷 docker-compose.yml、Caddyfile、.env.example、backup.sh 过来

# 3. 填配置
cp .env.example .env
chmod 600 .env
openssl rand -hex 32          # 把输出填进 .env 的 VM_JWT_SECRET
vi .env                       # 再填 PANEL_DOMAIN 与 SERVER_IMAGE

# 4. 起
docker compose up -d
docker compose logs -f server
```

日志里出现 `initial admin password (printed only once)` 那一行就是初始密码，**只打印这一次**。
拿它登录 `https://你的域名`，登录后到「设置 → 账号」改掉。

证书由 Caddy 自动签发续期，第一次访问可能要等几秒。

### 升级

```sh
cd /opt/vps-monitor
docker compose pull
docker compose up -d
```

数据在 `./data`（bind mount），升级不会动它；数据库迁移在服务端启动时自动跑。
agent 也会跟着升级——镜像里带着 agent 二进制，服务端启动时同步到 `./data/agent/`，
节点下次重跑安装命令拿到的就是新版本。已经装好的 agent 不会自动更新（步骤 20 才做），
但协议是兼容的，不升也能继续用。

### 回滚

```sh
# .env 里把 SERVER_IMAGE 的 tag 换成上一个版本，然后
docker compose up -d
```

注意：**迁移只增不改，不会回滚**。新版本如果加过表或列，退回旧镜像时那些东西还在，
旧代码不认识它们但也不碰。真正不兼容的改动会在发版说明里写清楚。

### 看日志

```sh
docker compose logs -f server          # 服务端，JSON 一行一条
docker compose logs -f caddy           # 证书签发、反代错误
docker compose ps                      # 容器状态，server 带 healthcheck
```

容器是 distroless 的，**里面没有 shell**，`docker compose exec server sh` 不可用。
服务端自己的子命令可以照常执行（`exec` 直接跑二进制）：

```sh
docker compose exec -T server /app/server version
docker compose exec -T server /app/server backup /data/backup/vm-手工.db
docker compose exec -T server /app/server reset-password admin
```

### 限制访问来源

`Caddyfile` 末尾注释里有一段 IP 白名单的写法。要注意 **agent 也要连 `/api/agent/ws`**，
白名单必须包含所有节点的出口 IP，否则节点会全部掉线。只想护住浏览器那一面的话，
给 `/dashboard` 和 `/api`（不含 `/api/agent/ws`）单独加白名单。

## 4. 备份与恢复

### 每天自动备份

`deploy/backup.sh` 调服务端的 `backup` 子命令（内部是 SQLite 的 `VACUUM INTO`），
写到 `./data/backup/vm-YYYY-MM-DD.db`，保留 14 天。

```sh
chmod +x /opt/vps-monitor/backup.sh
crontab -e
# 每天 03:00
0 3 * * * /opt/vps-monitor/backup.sh >> /var/log/vps-monitor-backup.log 2>&1
```

**不要直接拷 `vm.db`**：库开着 WAL，主文件旁边还有 `-wal` 和 `-shm`，
只拷主文件会丢掉尚未 checkpoint 的事务，拷出来的可能是个损坏的库。

### 恢复

完整步骤见 `deploy/restore.md`。要点：停 server → 把旧库挪开（别直接覆盖）→
**连 `-wal` 和 `-shm` 一起删** → 放上备份 → 起 server。

### 哪些东西需要保住

| 路径 | 内容 | 丢了会怎样 |
|---|---|---|
| `./data/vm.db` | 用户、节点、token、审计 | 一切配置都没了 |
| `./data/jwt.secret` | JWT 签名密钥（只在没设 `VM_JWT_SECRET` 时才有这个文件） | 所有人要重新登录 |
| `./data/agent/` | agent 二进制与安装脚本 | 不用管，服务端下次启动会从镜像里重新同步 |
| `./data/backup/` | 备份 | —— |
| Caddy 的 `caddy_data` 卷 | TLS 证书 | 会重新签发；反复重建有撞上 Let's Encrypt 限流的风险 |

## 5. 排障

### 忘记管理员密码

Docker 部署直接重置，不用停服务：

```sh
docker compose exec -T server /app/server reset-password admin
```

打印出来的新密码立即生效。下面那套「删掉用户重启重建」是没有这个子命令时的老办法，留作参考。

### 忘记管理员密码（旧办法）

初始密码只打印一次，没有找回途径。停掉服务，删掉 `users` 表里的 `admin`，再启动，服务端会重新创建并打印一个新密码：

```sh
sqlite3 data/vm.db "DELETE FROM users WHERE username = 'admin';"
```

### 登录返回 429

同一 IP 15 分钟内密码错 5 次会被锁 15 分钟（响应头 `Retry-After` 是剩余秒数）。等它过去，或者重启服务（计数在内存里，重启清零）。

改密码接口对旧密码的猜测也按同样规则限速，key 是用户而不是 IP。

### 登录返回 503「服务器繁忙」

同时进行的密码校验超过 4 个，排队超过 5 秒。argon2 每次要占 64 MiB 内存，这个闸是防止并发登录请求把内存打爆。正常使用碰不到；持续出现说明有人在打你的登录接口，去看 `sign-in rejected` 的 WARN 日志和来源 IP。

### 让所有已登录会话立即失效

删掉 `{VM_DATA_DIR}/jwt.secret` 后重启（或换一个 `VM_JWT_SECRET`），之前签发的 token 全部作废，浏览器会被踢回登录页并提示"登录已过期"。

注意：**改密码不会让已签发的 token 失效**，旧 token 最长还能用 12 小时。怀疑 token 泄露就用上面这招换密钥。

### 节点 token 丢了 / agent 认证失败

token 只在创建那一次显示，库里只有 sha256，找不回来。到「节点」页对着那台机器选「重置 token」，
会给出新 token 与新的安装命令；**旧 token 立刻失效**，那台机器上的 agent 需要用新 token 重装（或改 `/etc/vps-agent/config.yaml` 后重启）。

审计表能看到谁在什么时候重置的（不含 token 本身）：

```sh
sqlite3 data/vm.db "SELECT ts, actor, action, target_id, ip FROM audit_log WHERE action LIKE 'server.%' ORDER BY id DESC LIMIT 20;"
```

### 删节点会连带删掉什么

`servers` 是所有节点数据的根：删掉它，`server_host_info` 以及后续步骤的指标、ping 结果、入站、证书、修订、
订阅分配都会被外键级联删除，**不可恢复**（面板上有二次确认）。删之前想留数据就先备份 `vm.db`。
节点上的 agent 不会自己消失，要手动 `bash /path/uninstall.sh` 或停掉 systemd 服务，否则它会一直重连并被拒。

### agent 连不上面板

先看日志：`journalctl -u vps-agent -n 50`。

| 日志里的样子 | 原因与处理 |
|---|---|
| `服务端拒绝了 token（401）` | token 不对或已被重置。到面板「节点 → 重置 token」拿新的，重新跑一次安装命令即可（幂等） |
| `dial tcp ...: connection refused` / `i/o timeout` | 面板地址或端口不通：确认 `server` 写的是对外地址、DNS 解析正常、安全组放行 443 |
| `tls: failed to verify certificate` | 面板证书有问题（自签或过期）。生产用 Caddy 自动签发；临时调试可以把 `server` 换成 `ws://`（明文，只在内网用） |
| `连接断开，准备重连 ... retry_in=60s` 一直刷 | 退避已经到顶，说明长时间连不上。退避是 1→2→4…60 秒，日志不会刷屏；查上面几项 |

agent 断线期间照常采集、直接丢弃这一帧，**累计流量不会丢**（读的是网卡计数器）；恢复连接后数据自然接上。

### 节点显示离线 / 状态不刷新（步骤 05）

服务端这边的判定规则：收到 metrics 就算在线；**服务端收到的时刻**距今超过 15 秒判离线，每 5 秒扫一轮，所以 kill 掉 agent 后最坏 20 秒内面板会翻成离线。注意用的不是 agent 上报的 `ts`——节点时钟不准也不会影响判定。

先看服务端日志认不认这台 agent：

```sh
journalctl -u vps-server -n 100 | grep -E "agent connected|agent disconnected|踢掉"
```

| 现象 | 原因与处理 |
|---|---|
| 没有 `agent connected` | 连接压根没建立，转「agent 连不上面板」那一节 |
| `agent connected` 之后立刻 `agent disconnected` 且反复 | 多半是**两台机器共用了同一个 token**：一台节点只保留一条连接，新连接会把旧的踢掉（日志里有「踢掉同一节点的旧连接」，被踢方看到的关闭理由是 `superseded`），两边就会互相踢。给第二台单独建节点、拿自己的 token |
| 面板一直在线但数字不动 | agent 还连着但没在发 metrics。看 agent 日志有没有采集报错；服务端 30 秒收不到任何数据帧会主动断开，届时会出现 `agent disconnected` |
| 重置 token 后节点掉线 | 预期行为：重置会把在线的 agent 当场断开（鉴权只在握手时做一次）。在那台机器上重跑安装命令即可 |
| 浏览器页面不动但 `GET /api/servers` 是新的 | 浏览器的 WebSocket 没连上或没通过鉴权。`/api/ws` 要在连上后 5 秒内发 `{"type":"auth","token":"<accessToken>"}`，失败会以关闭码 **4001** 断开 |

反过来手工确认一台节点的实时状态，不用开浏览器：

```sh
curl -s -H "Authorization: Bearer $JWT" https://面板域名/api/servers   | python3 -c "import sys,json;[print(s['id'],s['name'],s['online'],s['last_seen']) for s in json.load(sys.stdin)['servers']]"
```

### agent 的数字对不上

```sh
vps-agent --once     # 不联网，直接打印 hello 与 metrics
```

- **速率比 `vnstat` / `iftop` 高很多**：多半是把虚拟网卡算进去了。看 `interfaces.exclude`，
  docker 网桥、veth 对、WireGuard 都要排除；改完 `systemctl restart vps-agent`。
- **磁盘容量不对**：`disk_mounts` 默认只统计 `/`。挂了数据盘就写成 `["/", "/data"]`，会求和（同设备去重）。
- **第一条 metrics 的 cpu 与速率是 0**：正常，差值算法要两次采样才有值。
- **`tcp` / `udp` / `procs` 是 0**：这三项读 `/proc`，容器里没挂 `/proc` 或非 Linux 系统上就会是 0。

### 历史曲线是空的 / 数据对不上（步骤 08）

秒级上报不直接落库，先在内存里按分钟聚合，每分钟第 2 秒写一次；每小时第 5 分钟把上一小时的
分钟行降采样成小时行，同时清理过期数据。所以：

| 现象 | 原因 |
|---|---|
| 刚接入的节点没有曲线 | 正常，要等第一次落库（最多一分钟）。详情页会显示「还没有历史数据」 |
| 曲线有缺口 | 那段时间节点掉线了。缺失的点**不补零**——补了会画出一条假的「CPU 掉到 0」 |
| 面板重启后丢了一分钟 | 正常，当前分钟的内存桶没写出去。只有这一分钟 |
| 切到 7 天 / 30 天没数据 | 那两档读小时表，要等整点后第 5 分钟的降采样跑过。新装的面板第一个小时内是空的 |
| 7 天前的分钟级曲线没了 | 正常，分钟表默认只留 7 天；小时表留 365 天 |

改保留期（单位是天，填非法值会被忽略）：

```sh
sqlite3 ./data/vm.db "INSERT INTO settings (key, value) VALUES ('retention.metrics_minute_days', '14')
  ON CONFLICT(key) DO UPDATE SET value = excluded.value;"
```

改完下一次清理（每小时第 5 分钟）生效，不用重启。

直接查库看聚合结果：

```sh
sqlite3 ./data/vm.db "SELECT datetime(ts,'unixepoch','localtime'), samples, round(cpu_avg,2), round(cpu_max,2)
  FROM metrics_minute WHERE server_id = 1 ORDER BY ts DESC LIMIT 10;"
```

`samples` 是这一分钟实际收到多少条上报，正常接近 60；明显偏小说明那一分钟节点在掉线重连。

### 看访问日志

每个请求一行 JSON，字段：`method`、`path`、`status`、`bytes`、`dur_ms`、`ip`、`req_id`。`/api/*` 记 `INFO`；静态资源与 `/api/health` 记 `DEBUG`，要看得 `VM_LOG_LEVEL=debug`。日志里不含 query 参数。

服务端 panic 会记一条 `panic recovered` 的 `ERROR`，带 `stack` 字段（完整堆栈）和 `req_id`，同时给客户端返回 500 `{"message":"服务器内部错误"}`。


## Ping 任务运维（步骤 09）

进入「设置 → Ping 任务」创建或编辑探测：ICMP 填 IP/域名，TCP 填主机:端口；默认每 60 秒，允许 10–3600 秒。启用“全部节点”会包含将来新增的节点；关闭后可多选节点，空选择代表不向任何节点下发。保存后显示配置进入发送队列的在线节点数，离线节点会在重连时收到最新配置。

首次探测随机等待 0–间隔秒，单次超时 3 秒。Linux 的 ICMP 使用原始套接字，需要 root 或 CAP_NET_RAW；原有 install.sh 以 root 启动 Agent。禁止 ICMP 的节点可改用 TCP。默认三个公网 IP 只作为示例，不保证所有机房都能访问。

卡片最新延迟/丢包率跟随 WS 快照；30 个方块每分钟刷新，最新在右：<100ms 绿、<200ms 黄、其余橙、超时红、尚无数据灰。离线节点显示最后记录。详情页“延迟”提供 1h/24h/7d/30d，左轴毫秒、右轴丢包百分比；断线没有样本的时段留空。

| 现象 | 检查 |
|---|---|
| 等待探测 | 节点是否在线、任务是否启用且作用范围包含该节点；等待一个间隔加 3 秒 |
| 一直超时 | Agent 的 `ping 探测失败` 日志（每任务最多 10 分钟一条）；手动 ping 目标或测试对应 TCP 端口 |
| 保存显示推送 0 台 | 当前没有可用发送队列；检查节点连接与服务端错误日志，离线重连会获取配置 |
| 卡片有数据，曲线暂时没有 | 等待 5 秒批量入库；若一直不出现，检查 `ping 结果落库失败，将重试` 与磁盘空间 |
| 禁用后任务行消失 | 正常；Agent 停止执行，卡片只展示启用任务，历史仍留库。删除任务才会删除历史 |
| 面板重启 | 已落库最近 30 点恢复；强杀前最后几秒未入库的数据不能保证恢复 |

保留期设置为 JSON 整数，默认 30 天，有效范围 1–3650：

```sh
sqlite3 ./data/vm.db "INSERT INTO settings (key,value) VALUES ('retention.ping_days','30') ON CONFLICT(key) DO UPDATE SET value=excluded.value;"
```

启动和每小时清理过期结果。队列超过 100000 条时会丢弃最老的待写结果并记录 ERROR；持续数据库错误应优先处理，重试不等于持久化保证。

本地自动化：`go vet ./proto/... ./agent/... ./server/...`、`go test ./proto/... ./agent/... ./server/...`；在 web 目录运行 `npm test`、`npm run tsc:check`、`npm run lint`、`npm run build`。前端测试使用 Node 24 自带测试运行器，没有额外测试依赖。竞态检测可在具备 CGO/C 编译器的环境运行 `go test -race ./agent/internal/ping ./server/internal/ping ./server/internal/hub`。

## 核心构建与版本管理（步骤 10）

当前钉定 sing-box v1.14.0，源码和标签约定见 [core-version.md](core-version.md)。运行本仓库 `sing-box` 工作流，输入稳定 tag。产出 amd64、arm64 两个 Linux 静态文件及 SHA256SUMS、许可证、对应源码；两架构的 version 与真实双用户统计测试通过才会发布 Release。

在「设置 → 代理核心」分别上传两架构文件，或输入公开 GitHub Release 文件链接下载；同时填写该次构建的 SHA256SUMS 对应值。官方默认包缺少 V2Ray API 标签，不能代替本项目构建产物。每个文件最多 64 MiB；同版本同架构不能换内容，要重建时应使用新的版本标识，或先删除未使用版本再重新上传。

两个架构齐全后点击「设为当前」并确认。面板复制本机 CPU 架构文件为 `/data/corefiles/current-local`，不会自动安装或升级节点；第 13 步已接通面板操作 API，操作页由第 14 步实现。Linux distroless 部署可验证：

```sh
docker compose exec server /data/corefiles/current-local version
```

Agent 下载请求需要 `Authorization: Bearer <agent token>`；校验返回头 `X-Checksum-Sha256` 与下载文件一致。不能用管理员 JWT 代替 Agent Token。支持断点续传与 ETag；重置 Token 后旧凭据不可继续下载。

数据库里的 `core.current_version` 是当前版本依据；每次启动按它恢复 current-local。备份数据库后，也应保留当前核心的版本目录（或同一次构建的 Release 资产与 SUMS）。只恢复数据库而没有对应核心时，启动会报告 `restore current core` 错误；先恢复整个版本目录再启动。需要撤销选择时，可在停机状态将设置的 JSON 值设为 `""`，再启动面板重新上传，不能只修改 current-local。

| 现象 | 处理 |
|---|---|
| SHA256 不匹配 | 检查是否选错架构、下载不完整，或混用了不同构建的 SUMS |
| 缺少标签 / 不是 Linux 静态文件 | 用文档中的自编译命令或本仓库工作流重新构建 |
| 同版本不能覆盖 | 当前资产不可变；切换到另一个完整版本后，才可删除旧版本 |
| 架构不全 | 补上传 amd64、arm64；只有一份时不能设为当前 |
| URL 被拒绝 | 使用公开 GitHub Release 文件链接；其他来源先在本地下载再上传 |
| 502 / 超时 | 检查面板到 GitHub 的连通性；当前接口不支持私有 Release 凭据 |
| Windows 下不能执行 current-local | 托管的是 Linux 核心，执行验证使用 Linux 容器或 Linux 面板 |

每用户统计复验使用 `ci/sing-box-stats`，完整命令与真实结果见 [验证记录](verify/sing-box-stats.md)。

## 15. Agent corectl（步骤 11）

Linux systemd 节点上使用 root 运行。Agent 配置可增加：

```yaml
core:
  stats_address: "127.0.0.1:10085"
```

只能填回环 IP:端口。sing-box 配置必须启用同地址的 v2ray_api 和对应用户/inbound 计数；具体字段见 core-version.md。核心服务日志由 systemd 收集至 `/var/log/sing-box/box.log`。

```sh
vps-agent core install --version v1.14.0 --sha256 <面板中本机架构的SHA256>
vps-agent core apply --file config.json --version v1.14.0 --ports 443/tcp,8443/udp
vps-agent core state
vps-agent core stats
vps-agent core logs --lines 200
vps-agent core stop
vps-agent core start
vps-agent core restart
```

所有命令可用 `--config /path/agent.yaml`；只有 install 需要面板和 token。apply 默认使用持久化修订号 +1，也可以 `--revision N` 指定。stdout 只输出 JSON，错误写 stderr 并返回非零退出码。Windows 下 core 子命令会明确报告仅支持 Linux/systemd。

安装只替换已校验的核心，不自动重启。配置 check 失败保留旧服务，端口缺失或重启失败会恢复旧配置；`core.state.applied_revision` 和 hash 只有成功后更新。发生配置失败时可以在本机运行 `sing-box check -c /path/config.json` 查看具体诊断，避免把包含密钥的校验输出发送到面板。

恢复记录在 `/var/lib/vps-agent/core-apply.json`，旧配置在 `/etc/sing-box/config.json.bak`。恢复失败时保留这些文件；修复 systemd/磁盘权限问题后重新启动 Agent 或重试 core 动作。手动 apply 与 Agent 下发通过进程文件锁互斥；锁忙时稍后重试。

统计会 reset，手动 `core stats` 会消费本次增量；诊断时应暂停 Agent 的统计采集，避免两个消费者分摊计数。离线期间保留计数在 sing-box 内存中，核心退出仍会丢失未采集增量。默认 systemd 在失败后 3 秒重启，10 秒轮询不一定能看见短暂退出；查看持续崩溃原因可读核心日志与 `journalctl -u sing-box`。

ufw/firewalld 的规则只添加，不回收。普通 Agent 卸载保留 sing-box、日志及核心状态；`uninstall.sh --purge-core` 才全部删除这些受管文件。该选项不撤销防火墙规则。

本地真实验收及安全清理步骤见 [verify/corectl.md](verify/corectl.md)。第 13 步已接通面板下发，核心操作 UI 仍由第 14 步实现。

## 16. 代理数据和凭据管理（步骤 12）

第 12 步提供管理员数据 API，第 13 步已接通异步下发；代理/订阅页面仍待第 14/15 步实现。完整字段和错误码见 [protocol.md 第 8 节](protocol.md#8-代理数据与凭据步骤-12)。新二进制启动时自动应用 0005/0006 迁移，不需要手工创建表；上线前按已有备份流程保存数据库。回退旧版本时恢复匹配的备份，不在有业务数据的库上直接执行删除九张表的 Down 迁移。

使用登录获得的管理员 JWT 调用以下接口（示例为请求体，不含真实凭据）：

```http
POST /api/servers/1/inbounds
{"protocol":"vless","listen_port":443}

POST /api/servers/1/inbounds
{"protocol":"hysteria2","listen_port":443,"settings":{"obfs_enabled":true}}

POST /api/subscribers
{"name":"家庭设备","reset_day":1}

PUT /api/subscribers/1/assignments
{"inbound_ids":[1,2]}

PUT /api/inbounds/1
{"enabled":false}
```

节点、用户与入站 ID 应替换为实际返回值。Reality 和 Hy2 可以同端口分别使用 TCP/UDP；SS2022 同时占用两者。禁用入站仍保留端口；要释放数据模型中的端口需删除入站，这也会删除对应用户分配。

| 操作 | 数据影响 |
|---|---|
| 入站 regenerate-keys | 旋转 Reality 密钥对与 short_ids、SS 服务端 PSK 或 Hy2 混淆密码；TUIC 应使用用户凭据/证书接口 |
| 证书 cert/regenerate | 同节点 Hy2/TUIC 共用的新自签证书和指纹；可传 sni 修改域名 |
| 用户 reset-token | 只更换订阅 URL 的 token，不修改三项连接凭据 |
| 用户 regenerate-credentials | 更换 uuid/password/ss_user_key，保留订阅 token |
| 用户 reset-usage | 清零当前用量及当前账期节点汇总，保留历史；只解除 quota 自动禁用，不解除 expired 或手动禁用 |
| assignments 传空数组 | 清空该用户全部入站分配；传错 ID 会整体失败，保留原分配 |

旋转凭据、证书和配置会在提交后触发合并渲染与下发；以 `GET /api/servers/{id}/core` 的 online、running、desired/applied、pending 和 last_error 判断实际结果。数据接口成功不代表 Agent 已应用。订阅 URL 服务仍由第 15 步实现，reset-token 只改订阅 token，不要求重启节点。

证书指纹用于后续客户端配置；证书为自签，API 不返回 key_pem。数据库备份包含代理私钥和用户凭据，按面板数据目录的敏感文件管理。管理员用户详情/创建/修改响应包含凭据，列表省略；代理接口都禁止缓存，审计会脱敏。

高级 JSON 通过 `PUT /api/servers/{id}/advanced` 的 extra_json 对象全量替换；传 `{ "extra_json": {} }` 清空。不可覆盖受管的 inbounds/experimental/log 或使用 outbound tag=direct。通过结构校验代表数据已保存，完整配置检查与可运行性由第 13 步验证。

本步本地验收结果见 [verify/proxy-model.md](verify/proxy-model.md)。测试使用独立 SQLite，未迁移或重启当前运行面板。

## 17. 面板核心闭环（步骤 13）

准备两架构托管核心并选为当前；等待 Agent 上线后，调用 `POST /api/servers/{id}/core/install`。收到 202 与 req_id 表示已入队，接着查看 GET core 的 installed_version。安装不会自动升级其它节点；对应节点版本与目标相同时，对齐器自动应用最新配置。

入站/用户/分配/证书/高级 JSON 的写入合并等待默认 5 s，之后预检、产生修订并下发。执行这些步骤需要额外时间，5 s 不是总应用超时。`POST .../core/apply` 可立即按当前数据渲染并重发，相同内容复用修订；Agent 离线时仍可生成 desired，重连后自动补发。

| 现象 | 检查与处理 |
|---|---|
| pending=true，提示目标版本未安装 | 显式调用 core/install；选择当前版本不会自动升级节点 |
| 预检失败，没有新修订 | 修正数据库中的入站/高级 JSON。接口不回传可能含密钥的核心 stderr；管理员可取完整修订或在受控 Linux 环境检查配置 |
| 端口应用失败，仍显示旧 applied_revision | Agent 已尝试恢复旧配置；查看 last_error 和 core/logs，检查被其它进程占用的端口 |
| Agent 无响应/三次重试耗尽 | 检查在线连接和本机核心；修复后手动 core/apply 或重连。重试为 10/30/60 s，单次等待 60 s |
| Windows 面板提示跳过预检 | Linux 核心不能在 Windows 执行；Agent 仍严格 check。部署到 Linux 面板可同时启用两端预检 |
| 配置里没有可用用户 | 确认 enabled、auto_disabled 和分配；SS 空用户保持拒绝旧用户凭据的多用户模式 |
| 用户传输了流量但面板还未变化 | Agent 约每 10 s 采集、面板每 60 s 落库，等待两个阶段；查看 Agent 统计错误和面板“代理流量落库失败” |
| 清零后历史日流量仍在 | 正常；清零只移除当前账期汇总，并隔离清零前未刷新的缓存，保留 daily 历史 |

`GET .../core/revisions` 查看最近 20 条；`GET .../core/revisions/{rev}` 含完整配置和凭据。`POST .../core/rollback/{rev}` 把旧内容作为新修订下发，使用旧修订的核心版本；若该资产已删除，先恢复对应版本。回滚不改入站/用户表，之后的数据修改或手动 apply 会恢复按当前数据渲染。仅重启面板不会撤销回滚。

`GET .../core/logs?lines=200` 从 Agent 取最多 1000 行、64 KiB，10 s 超时为 504，离线为 409。`POST .../core/restart` 返回 202 后继续观察 core 状态。所有接口需要管理员 JWT；Agent Token 只能接入 Agent 通道和下载资产。

真实生命周期、50 MiB 计费与清理证据见 [verify/reconciler.md](verify/reconciler.md)。正常关停会冲刷已接收的用量；统计无持久消息队列，强杀/断链不能保证精确一次。真实防火墙、公网 TLS 和 ARM64 真机验收仍待补。

## 18. 使用节点代理页（步骤 14）

1. 在“设置 → 代理核心”准备两架构文件并设置当前版本，打开“代理”总览，点击节点“管理”；监控卡片的更多菜单也可直达。
2. Agent 在线时点击“安装核心”并确认，观察已安装版本、运行状态和修订号；托管版本变化后逐台点击“升级到…”。安装/重启可能中断现有连接。
3. 新增 VLESS、SS、HY2、TUIC 入站，填写端口和协议设置。端口建议避开相同传输的已有入站，停用也保留占用；10085/tcp 用于核心统计。保存后默认先合并等待 5 s，再预检/下发，最后 applied 应追上 desired。订阅用户页和订阅输出在下一步接入。
4. 查看节点证书、生成密钥或完整配置时按管理员凭据对待。重生入站密钥/证书后需客户端刷新订阅；TUIC 使用用户凭据和节点证书。
5. 下发失败查看最后错误和日志（100/200/500 行），必要时在修订历史中查看 JSON 并回滚。回滚会创建新修订，**不修改入站表单**；后续编辑或重新下发会重新使用当前表单数据。

离线时可以保存配置和查看历史，核心操作/日志/回滚禁用。节点监听端口包含其它服务及本地统计接口；云安全组按代理入站实际所需协议/端口检查，勿把所有观察到的监听端口全部开放。

总览人数是含停用用户的分配人数；入站流量是面板本次运行累计，重启清零，不代表账期流量。前端显示“等待节点应用”表示数据库已保存，实际生效需查看核心状态。60 s 未确认时检查 Agent 连接、版本和错误后再操作。

本步本地验收与 fixture 边界见 [verify/proxy-ui.md](verify/proxy-ui.md)。
