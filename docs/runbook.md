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
bash uninstall.sh                    # 卸载（--purge-core 连 sing-box 一起删，步骤 11 起有用）
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

### 看访问日志

每个请求一行 JSON，字段：`method`、`path`、`status`、`bytes`、`dur_ms`、`ip`、`req_id`。`/api/*` 记 `INFO`；静态资源与 `/api/health` 记 `DEBUG`，要看得 `VM_LOG_LEVEL=debug`。日志里不含 query 参数。

服务端 panic 会记一条 `panic recovered` 的 `ERROR`，带 `stack` 字段（完整堆栈）和 `req_id`，同时给客户端返回 500 `{"message":"服务器内部错误"}`。
