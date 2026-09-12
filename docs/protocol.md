# 协议与接口

> 本文件随代码走：每个步骤新增或改动的 WebSocket 消息、REST 接口都记到这里。
> 设计方案（`设计方案.md`）不改，改了就记到对应步骤文档的"偏离记录"。

## 1. WebSocket · agent ↔ server

端点：`wss://{面板域名}/api/agent/ws`（服务端侧在步骤 05 实现）

### 1.1 握手

| 项 | 内容 |
|---|---|
| 鉴权 | 请求头 `Authorization: Bearer {agent token}`。token 是面板创建节点时给的 43 位 base64url 明文，服务端比对 sha256 |
| 版本 | 请求头 `X-Agent-Version: {agent 版本}` |
| 压缩 | `permessage-deflate`，context takeover |
| 失败 | 校验不过返回 401。agent 照常按退避重连，但日志会直说是 token 的问题，不会让人对着一串握手错误发呆 |

### 1.2 消息结构

消息是**扁平 JSON**：`type` 是消息自身的一个字段，不套 `data` 信封。收到一帧先解出 `type` 再按类型解成具体结构。

```json
{"type":"metrics","ts":1789199726,"cpu":11.26,"mem_used":34890579968,"...":"..."}
```

> 步骤 01 在这里写过一版 `{type, id, ts, data}` 的信封，与设计方案 §7 的报文不符，步骤 04 定稿时改回扁平。
> 需要请求/响应配对的消息（步骤 11 的 `core.logs`）自己带字段配对，不再依赖信封的 `id`。

时间戳 `ts` 一律 Unix 秒，字节一律整数，字段名蛇形。结构体见 `proto/msg.go`。

| 方向 | type | 说明 | 引入步骤 |
|---|---|---|---|
| agent → server | `hello` | 连上后第一条：协议版本、agent 版本、静态信息；之后每 5 分钟重发一次 | 04 |
| agent → server | `metrics` | 每 `report_interval` 秒一条 | 04 |
| agent → server | `ping` | ping 任务结果 | 09 |
| agent → server | `core.state` / `core.stats` / `core.logs` / `error` | sing-box 状态、流量、日志、执行失败 | 11 / 13 |
| server → agent | `config` | 上报间隔、ping 任务 | 04 |
| server → agent | `core.action` / `core.apply` | 安装启停、下发配置 | 11 / 13 |

### 1.3 `hello`（agent → server）

```json
{
  "type": "hello",
  "proto_version": 1,
  "version": "0.1.0",
  "applied_revision": 0,
  "host": {
    "hostname": "hk-01", "os": "Debian 12", "kernel": "6.1.0-21-amd64", "arch": "x86_64",
    "cpu_model": "AMD EPYC 7B13", "cores": 4,
    "mem_total": 8318000000, "disk_total": 93500000000, "boot_time": 1756100000,
    "ipv4": true, "ipv6": false
  }
}
```

- `proto_version` 是 `proto.Version`（当前 1），服务端用它做兼容判断。
- `applied_revision` 是已应用的 sing-box 配置修订号，步骤 11 之前恒为 0。
- `ipv4` / `ipv6` 是**出口可达性**，不是地址：agent 分别用 `tcp4` / `tcp6` 拨 `1.1.1.1:443` 与 `[2606:4700:4700::1111]:443`，3 秒超时。节点的公网 IP 由服务端从连接的来源地址记（步骤 05）。
- 采集不到的字符串字段填 `"unknown"`（LXC / OpenVZ 上 `host.Info()` 有些字段就是空的），数值字段填 0，不会因为单项失败整条消息缺席。

### 1.4 `metrics`（agent → server）

```json
{
  "type": "metrics", "ts": 1789199726,
  "cpu": 11.26, "mem_used": 1996000000, "swap_used": 0, "disk_used": 22800000000,
  "load": [0.47, 0.40, 0.35],
  "net": {"rx_total": 119000000000, "tx_total": 119000000000, "rx_rate": 15530, "tx_rate": 16502},
  "tcp": 86, "udp": 12, "procs": 143, "uptime": 1555200
}
```

| 字段 | 含义 |
|---|---|
| `cpu` | 0–100，两次 `cpu.Times` 差值算出（`(总增量 − idle − iowait) / 总增量`）。`guest` / `guest_nice` 不单独计入，Linux 已经把它们记在 `user` / `nice` 里 |
| `mem_used` / `swap_used` / `disk_used` | 字节。磁盘按配置的 `disk_mounts` 求和，同一设备只算一次，10 秒缓存 |
| `load` | 1 / 5 / 15 分钟负载 |
| `net.rx_total` / `tx_total` | 网卡累计字节，过滤掉 `interfaces.exclude` 里的网卡 |
| `net.rx_rate` / `tx_rate` | 字节每秒，按两次采样的差值除以实际间隔。计数器回绕或网卡被重建（新值 < 旧值）时记 0，不让曲线炸尖峰 |
| `tcp` / `udp` | `/proc/net/sockstat` 与 `sockstat6` 里的 `inuse` 之和（不是 `net.Connections`，那要遍历 `/proc/*/fd`，连接数上万时要几百毫秒） |
| `procs` | `/proc` 下的纯数字目录数 |
| `uptime` | 秒 |

**第一帧的 `cpu` 与两个 `*_rate` 恒为 0**：差值算法要两次采样才有值，秒级上报下只影响连上后的第一条。

### 1.5 `config`（server → agent）

```json
{"type": "config", "report_interval": 1,
 "ping_tasks": [{"id": 1, "name": "深圳电信", "target": "202.96.134.33", "kind": "icmp", "interval": 60}]}
```

- `report_interval` 单位秒，agent 侧夹取到 1–60；为 0 表示不改。收到后立即生效（下一帧按新间隔）。
- `ping_tasks` 步骤 09 才执行，本版本只记一条日志。

### 1.6 心跳与重连

| 项 | 值 |
|---|---|
| 心跳 | agent 每 20 秒发一次 WebSocket ping，10 秒内没回就断开重连（NAT 超时、对端假死都靠它发现） |
| 重连退避 | 1s → 2s → 4s … 60s 封顶 |
| 退避归零 | 连接**活过 30 秒**才算连上，退避才归零。否则遇到「能连上但立刻被关」会变成每秒重连一次 |
| 读上限 | 单帧 1 MiB |
| 写超时 | 5 秒 |

采集与连接是解耦的：采集协程按间隔一直采，没连上就丢弃当前这一帧（累计流量在网卡计数器里，断线期间的量不会丢）。

## 2. WebSocket · 浏览器 ↔ server

端点：`wss://{面板域名}/api/ws`

（步骤 05 起填充。）

## 3. REST

### 3.1 约定（步骤 02）

- 响应都是 JSON（`Content-Type: application/json; charset=utf-8`），204 除外。
- 错误统一为 `{ "message": string }`：参数错误 400、未登录或凭据无效 401、登录被限速 429（带 `Retry-After` 秒数）、路径不存在 404、方法不对 405、密码校验排队超时 503（带 `Retry-After`）、服务端错误 500。服务端 panic 也走这个格式（500），不会返回空 body。
- 鉴权：`Authorization: Bearer <accessToken>`。token 是 HS256 JWT，有效期 12 小时，claims `{ sub: "<用户ID>", name, role: "admin", iat, exp }`。
- 除 `GET /api/health`、`POST /api/auth/sign-in` 外，所有已注册的 `/api/*` 都要 token；缺失或无效一律 401 `{ "message": "unauthorized" }`。未注册的路径与不支持的方法在鉴权之前就返回 404 / 405。
- `/api/*` 下没匹配到的路径返回 JSON 404，不回退成 HTML；其余路径由内嵌前端处理（存在的静态文件直接返回，否则 `index.html`）。
- 请求体上限 1 MiB。
- 字段命名：**业务资源用蛇形**（`public_host`、`expire_at`），与 WebSocket 快照（§7.3）保持一致，前端一套类型两处复用。
  例外是 `/api/auth/*` 里的 `accessToken`、`user.displayName` 等，那是为了对齐前端 starter 的既有结构。
- 访问日志只记路径，不记 query。

### 3.2 接口

| 方法 | 路径 | 鉴权 | 说明 | 引入步骤 |
|---|---|---|---|---|
| GET | `/api/health` | 无 | `{ "ok": true, "version": "dev" }` | 01 |
| POST | `/api/auth/sign-in` | 无 | 登录。body `{ username, password }`（也接受 starter 的 `email` 字段名作为用户名）。200 `{ accessToken, expiresAt, user }`；401 `{ "message": "用户名或密码错误" }`；429 `{ "message": "尝试次数过多，请 N 分钟后再试" }` + `Retry-After` | 02 |
| GET | `/api/auth/me` | JWT | 当前用户 `{ user }` | 02 |
| POST | `/api/auth/password` | JWT | 改密码。body `{ oldPassword, newPassword }`：新密码 ≥ 10 个字符、≤ 256 字节、不能与旧密码相同。成功 204；当前密码不对 400 `{ "message": "当前密码错误" }`（不用 401，避免前端把会话清掉）；旧密码连错 5 次后 429 | 02 |
| GET | `/api/servers` | JWT | 节点列表 `{ servers: [...] }`。每项含实时状态字段 `online` / `last_seen` / `host`，步骤 05 之前分别恒为 `false` / `null` / `null` | 03 |
| POST | `/api/servers` | JWT | 新建节点。201 `{ server, token, install_command }`；`token` 是明文 agent token，**只在这一次出现** | 03 |
| GET | `/api/servers/{id}` | JWT | 单个节点 `{ server }`；节点不存在 404 `{ "message": "节点不存在" }` | 03 |
| PUT | `/api/servers/{id}` | JWT | 全量覆盖可写字段（没传的按缺省值），token 不受影响。200 `{ server }` | 03 |
| DELETE | `/api/servers/{id}` | JWT | 删除节点，子表靠外键级联删除。204 | 03 |
| POST | `/api/servers/{id}/token` | JWT | 重置 agent token，旧 token 立刻失效。200 `{ token, install_command }` | 03 |
| GET | `/install.sh` | 无 | agent 一键安装脚本，见 §3.4 | 03 |
| GET | `/agent/{file}` | 无 | agent 二进制与卸载脚本，见 §3.4 | 03 |

`expiresAt` 是 token 过期时刻的 Unix 秒。`user` 对象对齐前端 starter 的 `src/auth/types.ts`，`email` 字段放的是用户名（面板没有邮箱概念）：

```json
{ "id": 1, "displayName": "admin", "email": "admin", "photoURL": null, "role": "admin" }
```

### 3.3 登录限速与并发闸

同一客户端 IP 在 15 分钟窗口内失败 5 次即锁定 15 分钟：第 5 次失败仍返回 401，之后的请求（包括密码正确的）都返回 429，直到锁定到期。登录成功清零计数。计数只在内存里，重启服务即清零。

`POST /api/auth/password` 对旧密码的猜测用同一套规则，但 key 是用户 ID 而不是 IP。

同时进行的 argon2 校验上限 4 个，排队超过 5 秒返回 503。argon2id 每次要占 64 MiB 内存，没有这个闸的话几百个并发登录请求就能把进程打爆。

**客户端 IP 怎么取**：默认只认 TCP 连接地址，不采信任何请求头。放在反向代理后面时用 `VM_TRUSTED_PROXIES` 声明层数或可信网段，才会按 `X-Forwarded-For` 解析（详见 runbook）。

这里不使用 chi 的 `RealIP` —— 它会无条件采信 `True-Client-IP` / `X-Real-IP` 和 `X-Forwarded-For` 的**最左**值，而这些都是客户端能随手伪造的，Caddy 在前面也不会替你清掉；chi 官方已因此把 `RealIP` 标记为弃用（GHSA-3fxj-6jh8-hvhx 等）。用它的话，攻击者每次请求换一个 `True-Client-IP` 就能让登录限速彻底失效，并往审计日志里写任意来源 IP。

### 3.4 节点（步骤 03）

节点对象（`server`）的字段与 `servers` 表一一对应，另带三个实时状态字段：

```json
{
  "id": 1, "name": "香港-cst-01", "region": "HK", "group_name": "亚洲", "tags": ["V4", "V6"],
  "sort_order": 10, "public_host": "hk1.example.com",
  "price": 10.79, "currency": "USD", "billing_cycle": "month", "expire_at": "2026-09-24", "auto_renew": true,
  "traffic_limit": 4398046511104, "traffic_reset_day": 24, "traffic_mode": "max",
  "bandwidth_label": "1 Gbps", "note": "", "created_at": 1789196954, "updated_at": 1789196954,
  "online": false, "last_seen": null,
  "host": { "hostname": "hk-01", "os": "Debian 12", "kernel": "6.1.0", "arch": "x86_64",
            "cpu_model": "AMD EPYC 7B13", "cores": 4, "mem_total": 8318000000, "disk_total": 93500000000,
            "boot_time": 1756100000, "ipv4": true, "ipv6": true, "public_ip": "1.2.3.4",
            "agent_version": "0.1.0", "updated_at": 1789196954 }
}
```

写接口的校验（400 时 `message` 是中文提示）：

| 字段 | 规则 |
|---|---|
| `name` | 去空白后 1–64 字符，必填 |
| `region` | 空或两位 ISO 3166-1 alpha-2；小写自动转大写 |
| `group_name` | ≤ 32 字符 |
| `tags` | ≤ 10 个，每个 ≤ 16 字符；去空白、丢空串、按原顺序去重 |
| `sort_order` | 整数，±1 000 000 以内；列表按 `sort_order, id` 升序 |
| `public_host` | 空或域名 / IP，不带协议、端口、路径 |
| `price` | 0 ≤ price ≤ 1e9 |
| `currency` | 空（按 `CNY`）或三位 ISO 4217；小写自动转大写 |
| `billing_cycle` | `month` / `quarter` / `year` / `once`，空按 `month` |
| `expire_at` | `null`、空串或 `YYYY-MM-DD`（回写成标准格式） |
| `traffic_limit` | ≥ 0 的字节数，0 = 不限 |
| `traffic_reset_day` | 1–31，0 或缺省按 1 |
| `traffic_mode` | `out` / `in` / `sum` / `max`，空按 `max` |
| `bandwidth_label` | ≤ 32 字符 |
| `note` | ≤ 500 字符 |

**agent token**：32 字节随机数的 base64url（43 个字符）。库里只存 `sha256` hex，明文只在创建与重置的响应里出现一次，
列表与详情都拿不到；丢了只能重置。token 不用 argon2——它没有穷举空间，而 agent 每次重连都要校验一次。

**一键安装命令**（`install_command`）：

```sh
curl -fsSL {面板地址}/install.sh | bash -s -- --server {wss://面板地址}/api/agent/ws --token {token}
```

面板地址取 `VM_PUBLIC_URL`；没配就按这次请求的 `Host` 与协议推断（开发环境方便，反向代理后面必须配，
否则拼出来的是内网地址）。`https` → `wss`，`http` → `ws`。只有配了 `VM_TRUSTED_PROXIES` 时才采信 `X-Forwarded-Proto`。

**静态托管**：`GET /install.sh` 与 `GET /agent/{file}` 都是公开的（agent 装机时还没有任何凭据），
从 `{VM_DATA_DIR}/agent/` 下发，文件名白名单：

| 文件 | Content-Type |
|---|---|
| `install.sh`、`uninstall.sh` | `text/x-shellscript; charset=utf-8` |
| `vps-agent-linux-amd64`、`vps-agent-linux-arm64` | `application/octet-stream` |

白名单之外（含任何目录穿越写法）与文件不存在都返回 **纯文本 404**，不是 JSON 也不是 HTML——调用方通常是 `curl | bash`。
响应带 `Cache-Control: no-cache` 与 `Last-Modified`，支持 Range 续传。

---

## 4. 数据表

迁移文件在 `server/internal/store/migrations/`，goose 格式，只增不改。所有时间都是 Unix 秒。

### `0001_init.sql`（步骤 02）

| 表 | 字段 | 说明 |
|---|---|---|
| `users` | `id`, `username` UNIQUE, `password_hash`, `totp_secret`, `totp_enabled`, `created_at` | 密码是 argon2id PHC 串 `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`；TOTP 两列步骤 20 才用 |
| `settings` | `key` PK, `value` | `value` 是 JSON |
| `audit_log` | `id`, `ts`, `actor`, `action`, `target_type`, `target_id`, `before`, `after`, `ip` | `actor` 是用户名或 `system`；`before` / `after` 是 JSON；索引 `idx_audit_ts` |

### `0002_servers.sql`（步骤 03）

| 表 | 字段 | 说明 |
|---|---|---|
| `servers` | `id`, `name`, `region`, `group_name`, `tags`, `sort_order`, `token_hash` UNIQUE, `public_host`, `price`, `currency`, `billing_cycle`, `expire_at`, `auto_renew`, `traffic_limit`, `traffic_reset_day`, `traffic_mode`, `bandwidth_label`, `note`, `created_at`, `updated_at` | `tags` 是 JSON 字符串数组；`token_hash` 是 agent token 的 sha256 hex；`expire_at` 是 `YYYY-MM-DD` 或 NULL；索引 `idx_servers_sort(sort_order, id)` |
| `server_host_info` | `server_id` PK → `servers(id)` ON DELETE CASCADE, `hostname`, `os`, `kernel`, `arch`, `cpu_model`, `cores`, `mem_total`, `disk_total`, `boot_time`, `ipv4`, `ipv6`, `public_ip`, `agent_version`, `updated_at` | agent 上报的静态信息，每台节点最新一份；写入者是步骤 05 的 agent hub |

设计方案 §8.2 把 `server_host_info` 的公网地址写成 `public_ipv4` / `public_ipv6`，这里合成一列 `public_ip`：
agent 只探测 IPv4 / IPv6 的**可达性**（`ipv4` / `ipv6` 两个布尔列），真正的公网地址由服务端从 agent 的连接地址记一个。

### 审计动作

| action | target_type | 引入步骤 | 说明 |
|---|---|---|---|
| `auth.sign_in` | `user` | 02 | 登录成功 |
| `auth.password_change` | `user` | 02 | 改密码成功（不记录密码哈希） |
| `server.create` | `server` | 03 | 新建节点，`after` 是节点配置（不含 token 与哈希） |
| `server.update` | `server` | 03 | 改节点，`before` / `after` 是改前改后的配置 |
| `server.delete` | `server` | 03 | 删节点，`before` 是删前的配置 |
| `server.token_reset` | `server` | 03 | 重置 agent token，前后都不记（避免任何形式的 token 泄漏） |
