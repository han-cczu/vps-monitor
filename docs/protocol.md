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
| server → agent | `core.action` / `core.apply` / `core.logs` | 安装启停、下发配置、读取日志 | 11 / 13 |

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

- `proto_version` 是 `proto.Version`（当前 1）。服务端目前只在版本不一致时记一条 WARN，**不拒绝连接、也不降级**——协议只有一个版本，真要做兼容判断也无从判起。等到真的出第 2 版再定策略。
- `applied_revision` 是持久化的 sing-box 配置修订号；第 11 步起 Agent 在首次 hello 前恢复未完成的配置事务，随后 hello 上报最后成功修订号。每次连接在 hello 之后立即请求一次 core.state。
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
- `ping_tasks` 是该节点全部启用任务；每次 config 按 ID 对齐新增、变更和删除，空数组停止全部任务。

### 1.6 心跳与重连

| 项 | 值 |
|---|---|
| 心跳 | agent 每 20 秒发一次 WebSocket ping，10 秒内没回就断开重连（NAT 超时、对端假死都靠它发现） |
| 重连退避 | 1s → 2s → 4s … 60s 封顶 |
| 退避归零 | 连接**活过 30 秒**才算连上，退避才归零。否则遇到「能连上但立刻被关」会变成每秒重连一次 |
| 读上限 | 单帧 1 MiB |
| 写超时 | 5 秒 |

采集与连接是解耦的：采集协程按间隔一直采，没连上就丢弃当前这一帧（累计流量在网卡计数器里，断线期间的量不会丢）。

### 1.7 服务端侧行为（步骤 05）

| 项 | 行为 |
|---|---|
| 鉴权 | 握手时读 `Authorization: Bearer <agent token>` → sha256 → 查 `servers.token_hash`。失败一律 401（查库出错也回 401，不给无效 token 多一个信号） |
| 连上即下发 | 立刻发一条 `config`：`report_interval: 1`、`ping_tasks` 为适用于该节点的启用任务 |
| 单连接 | 一台节点同时只保留一条连接。新连接进来会把旧的关掉，关闭码 1000、理由 `superseded`。**两个 agent 共用一个 token 会互相踢**，但每条连接活不过 30 秒，退避会一路涨到 60 秒，不会打满 CPU |
| 沉默超时 | 30 秒没收到任何数据帧就断开（ping 不算数据帧）。正常情况下每秒都有 metrics |
| 坏消息 | 解不开的 JSON、未知类型只记 WARN 丢弃，不断连接——一条坏消息不该让整台节点掉线 |
| `public_ip` | 由服务端从连接地址记录（按 `VM_TRUSTED_PROXIES` 决定是否采信 `X-Forwarded-For`），不采信 agent 自报 |
| `hello` 落库 | 写 `server_host_info`（幂等 upsert），agent 每 5 分钟重发一次 |
| 在线判定 | **握手成功**即标记在线并把 `last_seen` 置为当前时刻，之后每条 metrics 刷新一次；`last_seen` 距今超过 15 秒 → 离线，每 5 秒扫一轮，所以最坏 20 秒内能看到状态变化。连接断开本身不立刻置离线（重连、滚动重启都会短暂断开，立刻翻状态会让卡片闪）。连上但一条 metrics 都不发的 agent，15–20 秒后照样判离线 |
| 节点被删除 | 在途的 hello / metrics 不会把已删节点的内存态复活，直接丢弃并记 WARN |
| token 重置 | `POST /api/servers/{id}/token` 会把该节点在线的 agent 当场断开 |

## 2. WebSocket · 浏览器 ↔ server

端点：`wss://{面板域名}/api/ws`（开发时 Vite 代理 `/api/ws`，见 `web/vite.config.ts`）

### 2.1 鉴权：首帧 `auth`

浏览器的 WebSocket API 没法自定义请求头，带不了 `Authorization`，所以 JWT 走**首帧**：

```json
{ "type": "auth", "token": "<和 REST 用的同一个 accessToken>" }
```

- 校验通过：服务端立刻推一帧 `snapshot`（不用等下一个整秒），之后每秒一帧。
- 5 秒内没收到首帧、首帧不是 `auth`、或 token 无效：关闭连接，**关闭码 4001**，理由 `unauthorized`。
- 连上之后浏览器不需要再发任何消息；发了也会被丢弃（留给后续步骤扩展订阅指令）。
- 单帧上限 8 KiB（首帧之外没有别的输入）。

### 2.2 `snapshot`（server → 浏览器，步骤 05）

每秒一帧全量快照。不做增量：十几台节点的全量帧压缩前不到 9 KB，增量协议的复杂度不值当。

```json
{"type":"snapshot","ts":1757660000,"servers":[
  {"id":1,"name":"深圳-阿里云-01","region":"CN","group":"","tags":[],"sort":0,
   "online":true,"last_seen":1757660000,"v4":true,"v6":false,
   "cpu":3.02,"cores":2,"mem":{"used":458000000,"total":1690000000},"swap":{"used":0},
   "disk":{"used":9720000000,"total":42000000000},"load":[0.04,0.03,0],
   "net":{"up":303,"down":169,"out_total":126900000,"in_total":1557000000},
   "conn":{"tcp":23,"udp":4},"procs":112,"uptime":172800,
   "expire_at":"2027-09-10","bandwidth":"3Mbps","price":99,"currency":"CNY","cycle":"year",
   "traffic":null,"ping":[],"core":null}
]}
```

| 项 | 说明 |
|---|---|
| 顺序 | 按 `sort_order`、`id` 升序，与 `GET /api/servers` 一致 |
| 字段名 | 刻意比 REST 短（`group` / `sort` / `cycle` / `bandwidth`），每秒一帧，字段名占的字节比数值还多 |
| 数据来源 | 配置字段来自 `servers` 表（60 秒缓存，增删改会立刻失效）；实时字段来自内存态 |
| `last_seen` | 服务端**收到** metrics 的时刻（Unix 秒），不是 agent 上报的 `ts`——节点时钟不一定准。握手成功也会把它置为当时时刻。从没连过是 `null` |
| 掉线的节点 | 保留最后一次的数值（`cpu` / `mem` / `net` 等不归零），前端置灰显示即可 |
| 没上报过的节点 | 实时字段是零值，`last_seen` 为 `null` |
| 服务端刚重启 | 启动时会用 `server_host_info` 表预热 `cores` / `mem.total` / `disk.total` / `v4` / `v6`，所以 agent 还没重连也不会显示成 0；真正从没上报过的节点这些字段才是 0 |
| `traffic` / `ping` / `core` | `traffic` / `core` 暂为 `null`；`ping` 是任务摘要数组，无适用任务时为 `[]` |
| 压缩 | `permessage-deflate`（context takeover），两端都协商 |

`ServerView` 定义在 `server/internal/hub/state.go`，不在 `proto` 包里：`proto` 是 agent 与 server 共享的零依赖包，而快照里带着价格、账期这类只属于面板的字段，agent 不该知道。这条 Go ↔ TypeScript 的契约由 `server/internal/hub/testdata/snapshot.json` 的 golden 测试守着，改字段名会先让测试变红。

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
| GET | `/api/servers` | JWT | 节点列表 `{ servers: [...] }`。每项含实时状态字段 `online` / `last_seen`（步骤 05 起来自 hub 内存态）与 `host`（agent hello 上报后入库） | 03 |
| POST | `/api/servers` | JWT | 新建节点。201 `{ server, token, install_command }`；`token` 是明文 agent token，**只在这一次出现** | 03 |
| GET | `/api/servers/{id}` | JWT | 单个节点 `{ server }`；节点不存在 404 `{ "message": "节点不存在" }` | 03 |
| PUT | `/api/servers/{id}` | JWT | 全量覆盖可写字段（没传的按缺省值），token 不受影响。200 `{ server }`，含 `online` / `last_seen` / `host` | 03 |
| DELETE | `/api/servers/{id}` | JWT | 删除节点，子表靠外键级联删除。204 | 03 |
| POST | `/api/servers/{id}/token` | JWT | 重置 agent token，旧 token 立刻失效；**在线的 agent 会被当场断开**（鉴权只在握手时做过一次）。200 `{ token, install_command }` | 03 |
| GET | `/api/servers/{id}/history` | JWT | 历史曲线，见 §3.5。query `range` 取 `1h` / `24h` / `7d` / `30d`（缺省 24h）；非法值 400 | 08 |
| GET | `/api/agent/ws` | agent token | agent 接入（WebSocket 升级）。`Authorization: Bearer <agent token>`，见 §1；token 不对 401 | 05 |
| GET | `/api/ws` | 首帧 auth | 浏览器接入（WebSocket 升级），见 §2；鉴权失败关闭码 4001 | 05 |
| GET | `/install.sh` | 无 | agent 一键安装脚本，见 §3.4 | 03 |
| GET | `/agent/{file}` | 无 | agent 二进制与卸载脚本，见 §3.4 | 03 |
| GET | `/agent`、`/agent/` | 无 | 不带文件名，返回纯文本 404（不落到 SPA 回退给 curl 一段 HTML） | 03 |

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

### 3.5 历史曲线（步骤 08）

`GET /api/servers/{id}/history?range=24h`

```json
{"step":60,"from":1757600000,"to":1757686400,
 "mem_total":51370958848,"disk_total":1024191361024,
 "points":[{"ts":1757600000,"cpu":3.1,"cpu_max":8.2,"mem":458000000,"swap":0,"disk":9720000000,
            "load1":0.04,"rx":169,"rx_max":420,"tx":303,"tx_max":900,"tcp":23,"udp":4,"procs":112}]}
```

| 项 | 说明 |
|---|---|
| `range` | `1h` / `24h` 读分钟表（`step` 60），`7d` / `30d` 读小时表（`step` 3600）。30 天的分钟行有 4 万多条，画出来既慢又没有意义 |
| 字段名 | 比库里的短（`cpu` 而不是 `cpu_avg`）：24 小时是 1440 个点，字段名占的字节比数值还多 |
| `cpu` / `mem` / `rx` / `tx` | 区间**平均**；`cpu_max` / `rx_max` / `tx_max` 是区间**峰值** |
| `disk` / `tcp` / `udp` / `procs` | 区间内**最后一条**的值。磁盘用量是水位不是速率，平均出来的数既不是起点也不是终点 |
| 缺失的点 | **不补零**。节点掉线那几分钟本来就没有数据，补成 0 会在曲线上画出一段「CPU 掉到 0」的假象；前端用 datetime 轴，缺口自然留空 |
| `mem_total` / `disk_total` | 画百分比用，来自 `server_host_info`；节点从没连过就是 0 |
| 浮点 | 服务端已经四舍五入到两位小数，前端不用再处理 |

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

### `0003_metrics.sql`（步骤 08）

| 表 | 内容 |
|---|---|
| `metrics_minute` | 秒级上报按分钟聚合的结果，`(server_id, ts)` 复合主键 + `WITHOUT ROWID`，`ts` 是整分。默认留 7 天 |
| `metrics_hour` | 分钟行按小时降采样，结构与分钟表相同，`ts` 是整点。默认留 365 天 |

两张表都随 `servers` 级联删除。保留天数可以改 `settings` 里的 `retention.metrics_minute_days` 与
`retention.metrics_hour_days`（填非法值会被忽略并回落到默认，免得一次把历史删光）。

秒级数据不落库：十几台节点每秒各一条，一天一百多万行，而面板要看的是曲线。
聚合在内存里做，每分钟写一次；进程重启会丢掉当前这一分钟还没写出去的桶。

### 审计动作

| action | target_type | 引入步骤 | 说明 |
|---|---|---|---|
| `auth.sign_in` | `user` | 02 | 登录成功 |
| `auth.password_change` | `user` | 02 | 改密码成功（不记录密码哈希） |
| `server.create` | `server` | 03 | 新建节点，`after` 是节点配置（不含 token 与哈希） |
| `server.update` | `server` | 03 | 改节点，`before` / `after` 是改前改后的配置 |
| `server.delete` | `server` | 03 | 删节点，`before` 是删前的配置 |
| `server.token_reset` | `server` | 03 | 重置 agent token，前后都不记（避免任何形式的 token 泄漏） |


## 5. Ping 任务（步骤 09）

所有 REST 接口需要管理员 JWT。任务以 `sort_order, id` 排序。

| 方法与路径 | 请求 / 响应 |
|---|---|
| `GET /api/ping-tasks` | `{tasks: PingTask[]}`，含禁用任务 |
| `POST /api/ping-tasks` | 创建，201 `{task, pushed}` |
| `PUT /api/ping-tasks/{id}` | 全量更新可写字段，200 `{task, pushed}` |
| `DELETE /api/ping-tasks/{id}` | 204；历史结果级联删除 |
| `GET /api/servers/{id}/ping/recent?n=30` | `{tasks:[{task_id,name,results:[{ts,latency}]}]}`；`n` 为 1–30，默认 30 |
| `GET /api/servers/{id}/ping/history?task={id}&range=24h` | `{step,from,to,task_id,name,points:[{ts,avg,max,loss}]}` |

PingTask 可写字段：`name`（1–32 字）、`target`（icmp 为 IP/域名，tcp 为 host:port，IPv6 带方括号）、`kind`（icmp/tcp）、`interval_sec`（10–3600）、`server_ids`（null 为全部，包括未来新增节点；[] 为不作用于任何节点；ID 为正数且不能重复）、`enabled`（默认 true）、`sort_order`（±1000000）。响应另含 `id`、`created_at`、`updated_at`。

`pushed` 表示配置已进入多少台在线节点的发送队列，不是探测成功数，也不是 Agent 执行确认。离线节点重连时获取最新配置。API 写库后立即刷新缓存并推送；任务服务每 5 秒复核任务表，补偿短暂数据库错误导致的缓存刷新失败。

Agent 上报：

```json
{"type":"ping","task_id":1,"ts":1789362482,"latency_ms":12.3}
```

`latency_ms:null` 表示探测失败或超时。服务端使用接收时刻作为记录时间，仅接受适用于该节点的启用任务。相同 `(server_id, task_id, ts)` 覆盖，不重复计入最近窗口。

快照每任务一项：

```json
{"task_id":1,"name":"电信","latency":12.3,"loss":3.33,"last_ts":1789362482}
```

`last_ts:null` 表示尚无探测数据，与 `latency:null` 且 `last_ts` 有值的超时区分。`loss` 为最近最多 30 次实际探测的丢包百分比；没有数据时为 0，但界面显示等待探测。

历史分桶：1h / 24h 每分钟，7d 每 10 分钟，30d 每小时。`avg/max` 只统计成功样本；全丢包桶为 null，`loss=100`；完全没有样本的桶不返回。前端补 null 断开缺测时段，不补成成功或丢包。

`0004_ping.sql` 新增 `ping_tasks` 与 `ping_results`。任务 ID 使用 AUTOINCREMENT，防止删除后复用 ID 时旧的在途结果写到新任务。结果表主键 `(server_id,task_id,ts)`、WITHOUT ROWID，随节点或任务级联删除。默认三网目标为示例地址，需按节点实测调整。新增审计动作 `ping_task.create/update/delete`，目标类型为 `ping_task`。

结果每 5 秒一个事务落库，失败批次放回队列等待重试；已经删除的任务/节点结果跳过，不使整批回滚。待写队列最多 100000 条，超出时丢弃最老结果并记 ERROR。正常关闭会等待最后一次写入，强制杀进程仍可能丢失尚未落库的数据。

启动先清理过期结果并预热窗口；首次 recent 请求会把数据库完整 30 点与已到达的新结果合并，`n` 仅裁剪响应。保留期由 `retention.ping_days` 控制，默认 30 天，有效范围 1–3650；每小时清理。

## 6. 核心构建托管（步骤 10）

管理员接口使用 JWT，下载接口使用节点 Agent Token。两种凭据不互通。

| 方法与路径 | 请求 / 响应 |
|---|---|
| `GET /api/corefiles` | `{versions:[{version,arches,files,uploaded_at,current}],pinned_version}` |
| `POST /api/corefiles` | multipart：version、arch、sha256、file，每次一个文件；201 `{version,file}` |
| `POST /api/corefiles/fetch` | JSON `{version,arch,sha256,url}`；从公开 GitHub Release 获取；201 `{version,file}` |
| `PUT /api/corefiles/current` | JSON `{version}`；200 `{version}`，两个架构须齐全且校验通过 |
| `DELETE /api/corefiles/{version}` | 204；当前版本返回 409 |
| `GET /api/agent/corefiles/{version}/{arch}` | Agent Bearer Token；文件流，支持 Range / If-None-Match |

版本必须是 `vMAJOR.MINOR.PATCH` 稳定标签形式，单段最多四位数字；架构为 amd64 或 arm64。`files` 中每项为 `{arch,sha256,size,uploaded_at}`，时间为 Unix 秒；sha256 为小写十六进制。文件上限 64 MiB，超出返回 413；损坏校验和、错误构建标签或 ELF 架构返回 400；同版本同架构内容冲突或架构不全返回 409；缺失文件为 404。

下载的 `ETag` 为带双引号的 SHA256，`X-Checksum-Sha256` 为同一值（不带引号），`Cache-Control: private, no-cache`。条件命中 304、范围请求 206 都先验证 Agent Token。Token 重置和节点删除立即影响后续下载。

目录是 `{DataDir}/corefiles/{version}/sing-box-linux-{arch}`、`SHA256SUMS`、`manifest.json`。校验后原子发布清单；版本架构内容不可变，同 SHA256 重传幂等。当前版本保存在 `settings['core.current_version']` 的 JSON 字符串，`current-local` 是与面板 CPU 架构一致的 Linux 静态二进制，模式 0755。启动按设置重建派生文件；缺失或损坏的当前核心会使恢复失败并记录错误。

URL 获取只接受 `https://github.com/{owner}/{repo}/releases/download/{tag}/{file}`，允许重定向到 GitHub Release 资产域名，不转发管理员或 Agent 凭据。传输最多两个并发、两分钟超时。上传只读 buildinfo/ELF，不运行核心，不向在线节点发送升级命令。

新增审计 action：`corefile.upload`、`corefile.fetch`、`corefile.set_current`、`corefile.delete`；target_type 为 `corefile`，记录版本和产物元数据，不记录下载签名 URL 或凭据。

## 7. Agent 核心管理消息（步骤 11）

Agent 接收端与第 13 步服务端渲染、下发、上报入库均已接通。字段采用 `proto/core.go` 的蛇形 JSON，未知字段、类型或动作拒绝。未定义的顶层消息仅记 WARN。

```json
{"type":"core.action","action":"install","version":"v1.14.0","file":"sing-box-linux-amd64","sha256":"<64位小写SHA256>","req_id":"install-1"}
{"type":"core.action","action":"restart","req_id":"restart-1"}
{"type":"core.apply","core":"sing-box","revision":1,"version":"v1.14.0","config_sha256":"<compact JSON的SHA256>","ports":["443/tcp","8443/udp"],"config":{},"req_id":"apply-1"}
{"type":"core.logs","kind":"error","lines":200,"req_id":"logs-1"}
```

- `core.action` 仅 install/start/stop/restart；非 install 不接受 version/file/sha256。install 使用 Agent 配置里的面板 origin 和 Bearer token，仅下载 `/api/agent/corefiles/{version}/{arch}`，不跟随重定向。version 必须 `vX.Y.Z`；file 可省略，提供时必须与本机架构相符；不自动升级或重启已运行的核心。
- `core.apply` 的 Config 是小于 1 MiB 的 JSON 对象，使用 Go `json.Compact` 后计算小写 SHA256。字段顺序会影响 hash。revision 为正数且单调不减，同 revision 不得换 hash；同 revision 重放会检查磁盘 hash、进程与端口。不同版本必须先显式安装。回滚内容应以新 revision 下发。
- ports 最多 1024 项，格式为 `1..65535/tcp|udp`，不接受重复、前导零或任意命令片段。安装、配置与启停只有一个执行 worker，最多等待 4 项；队列满与校验失败返回带 error 的 core.state。拒绝消息不运行外部命令。
- req_id 可选、最多 128 字节，用于 core.state 或 core.logs 关联请求。日志 kind 目前只有 error，lines 1–1000，单次文本最多 64 KiB。

```json
{"type":"core.state","core":"sing-box","installed_version":"v1.14.0","running":true,"applied_revision":1,"config_sha256":"<SHA256>","listening":["443/tcp"],"firewall":"none","error":null,"req_id":"apply-1"}
{"type":"core.stats","ts":1789369552,"inbounds":[{"name":"ss-test","up":85,"down":1000117}],"users":[{"name":"sub-1","up":85,"down":1000117}]}
{"type":"core.logs","kind":"error","text":"...","req_id":"logs-1"}
```

- core.state 在 hello 后、执行完成、每 60 秒与每 10 秒轮询发现 running 变化时上报。listening 来自本机 `/proc/net/tcp{,6}`、`udp{,6}`，不代表独占端口归属。firewall 为 ufw/firewalld/none；应用只添加规则，回滚不删除放行规则。error 最多 2 KiB，配置校验 stderr 不透传，避免回传密钥。
- core.stats 每 10 秒从回环地址调用 `/v2ray.core.app.stats.command.StatsService/QueryStats`，reset=true。up/down 是客户端上传/下载的字节增量；未知计数器或负数忽略。连接失败后等待 60 秒重试，错误通过 core.state 回传。
- 离线不查询/reset；发送失败保留当前批次直到重发成功。没有应用层 ACK 或持久队列，进程退出或 reset 后网络中断仍有丢失/确认边界，不保证精确一次。
- 配置应用先 check，再备份、替换、放行、restart、检查端口；失败恢复旧配置和修订。首次应用没有旧配置则 stop 并删除失败配置。未完成事务留在 `/var/lib/vps-agent/core-apply.json`，Agent 启动恢复后才发 hello。

## 8. 代理数据与凭据（步骤 12）

以下 20 个方法/路径组合均需要管理员 JWT，Agent Token 不可访问；响应使用 `Cache-Control: no-store`。第 12 步建立数据接口，第 13 步已把提交后的变更通知接入对齐器；保存成功表示数据落库，实际下发/应用状态见第 9 节。

| 方法与路径 | 请求 / 响应 |
|---|---|
| `GET /api/servers/{id}/inbounds` | `{inbounds: Inbound[]}`，含禁用项，按 ID 排序 |
| `POST /api/servers/{id}/inbounds` | Inbound 可写字段；201 `{inbound}` |
| `GET /api/inbounds/{id}` | `{inbound}` |
| `PUT /api/inbounds/{id}` | 部分更新可写字段；`{inbound}` |
| `DELETE /api/inbounds/{id}` | 204，级联删除分配 |
| `POST /api/inbounds/{id}/regenerate-keys` | 空体或 `{}`；`{inbound}`；TUIC 返回 400 |
| `GET /api/servers/{id}/cert` | `{cert}`，尚未生成时为 null；永不返回私钥 |
| `POST /api/servers/{id}/cert/regenerate` | `{sni?}` 或空体；`{cert}` |
| `GET /api/servers/{id}/advanced` | `{advanced:{server_id,extra_json,updated_at}}`，未设置时 extra_json 为 `{}`、updated_at 为 null |
| `PUT /api/servers/{id}/advanced` | `{extra_json:{...}}` 全量替换；`{advanced}` |
| `GET /api/servers/{id}/core` | `{core: NodeCore}`，第 13 步增加实时字段，见第 9 节 |
| `GET /api/subscribers` | `{subscribers: Subscriber[]}`，不含四项凭据，按 ID 排序 |
| `POST /api/subscribers` | Subscriber 可写字段；201 `{subscriber}` |
| `GET /api/subscribers/{id}` | `{subscriber}`，含四项凭据 |
| `PUT /api/subscribers/{id}` | 部分更新可写字段；`{subscriber}` |
| `DELETE /api/subscribers/{id}` | 204，级联删除分配和该用户流量记录 |
| `PUT /api/subscribers/{id}/assignments` | `{inbound_ids:[1,2]}` 全量替换；`{subscriber}` |
| `POST /api/subscribers/{id}/reset-token` | 空体或 `{}`；只旋转 sub_token，返回 `{subscriber}` |
| `POST /api/subscribers/{id}/regenerate-credentials` | 空体或 `{}`；只旋转 uuid/password/ss_user_key，返回 `{subscriber}` |
| `POST /api/subscribers/{id}/reset-usage` | 空体或 `{}`；清零当前用量，返回 `{subscriber}` |

除创建和删除外，成功返回 200。错误格式为 `{message}`：字段/格式错误 400、未认证 401、对象不存在 404、端口冲突 409、内部错误 500。请求体最大 1 MiB，须为单个 JSON 对象；未知可写字段与尾随 JSON 拒绝。除 `expire_at` 外，可写顶层字段不能为 null。

### Inbound

可写字段为 `protocol`、`listen_port`、`settings`、`remark`、`enabled`。创建必须有 protocol 与 1–65535 的 listen_port，默认 enabled=true；PUT 省略字段保留旧值，settings 按字段合并，protocol 不可更改。响应另含 `id`、`server_id`、`tag`、`created_at`、`updated_at`，不可通过写接口提交这些字段。tag 按协议生成为 `vless/ss/hy2/tuic-{port}`；时间为 Unix 秒。

| protocol | settings 字段与默认值 | 占用传输 |
|---|---|---|
| vless | handshake_server=`www.microsoft.com`、handshake_port=443；自动生成 private_key/public_key/short_ids | TCP |
| shadowsocks | method 固定 `2022-blake3-aes-128-gcm`；自动生成 server_psk | TCP + UDP |
| hysteria2 | obfs_enabled=false、自动生成 obfs_password；up_mbps/down_mbps=0、ignore_client_bandwidth=false | UDP |
| tuic | congestion_control=`bbr`（另支持 cubic/new_reno）、zero_rtt=false | UDP |

同节点同端口的传输不能重叠，禁用仍保留端口。例如 vless:443 与 hysteria2:443 可并存，shadowsocks:443 与任一其它协议冲突。检查受 SQLite 事务与 INSERT/UPDATE trigger 共同保护。

settings 最大 64 KiB，不接受未知字段和 null 字段；省略密钥时创建自动生成、更新保留。Reality 公私钥为 32 字节无填充 base64url，公私钥必须匹配；显式提供私钥而省略公钥时自动推导。short_ids 为 1–16 个不重复的小写 hex，每项长度 2–16 且为偶数。SS PSK 为 16 字节标准 base64。Hy2 带宽范围为 0–1000000 Mbps，启用混淆时密码不能为空。regenerate-keys 旋转 Reality 密钥对和 short_ids、SS 服务端 PSK 或 Hy2 混淆密码；TUIC 使用用户凭据和节点证书，没有独立入站密钥。

### Cert 与 NodeCore

首次创建 Hy2/TUIC 入站自动生成节点证书，默认 SNI=`www.bing.com`；节点上的这两种协议共享证书。重新生成时省略 sni 或传空字符串，保留已有 SNI；没有旧证书则使用默认值。SNI 只接受 ASCII DNS 域名，不接受 IP、URL、端口和通配符。

Cert 响应字段为 `server_id`、`sni`、`cert_pem`、`fingerprint_sha256`、`not_after`、`created_at`，两个时间为 Unix 秒。证书为 ECDSA P-256 自签，SAN 包含 SNI，有效期为生成时刻前 1 小时至后 3650 天。指纹是 DER 的 SHA256、大写 hex、冒号分隔。key_pem 只保存在数据库，API 不返回。

NodeCore 含 `server_id`、`core`、`desired_version`、`installed_version`、`running`、`applied_revision`、`desired_revision`、`config_sha256`、`listening`、`firewall`、`last_error`、`updated_at`。既有和新建节点都会初始化：core=sing-box、running=false、修订号=0、listening=[]，其余可空字段为 null。第 13 步开始由 Agent core.state 更新实际状态，离线时保留最后观察值。

### Subscriber 与高级 JSON

Subscriber 可写字段为 `name`（1–64 字）、`note`（最多 2000 字）、`enabled`（默认 true）、`traffic_limit`（0–9007199254740991 字节，默认 0）、`reset_day`（0–31，默认 0）、`expire_at`（YYYY-MM-DD，省略保留，null/空字符串清空）。限额、重置日和到期日的自动执行由步骤 16 实现。

响应另含 `id`、`auto_disabled`、`traffic_used`、`period_start`、`created_at`、`updated_at`、`assigned_inbounds:[{inbound_id,server_id,server_name,protocol,port}]` 与去重节点数 `servers_count`。新用户 auto_disabled=none、traffic_used=0，period_start 为面板时区当日零点的 Unix 秒。四项凭据 `uuid`、`password`、`ss_user_key`、`sub_token` 由服务端生成，只有列表接口省略；不能通过普通 PUT 修改。

分配最多 1000 项，ID 必须为正数、不重复且入站存在，`[]` 清空，省略或 null 拒绝；任何无效 ID 都不改变原分配。提交后通知变更前后涉及的节点，按节点去重。reset-token 保持三项代理凭据；regenerate-credentials 保持订阅 token。reset-usage 将 traffic_used 清零并删除当前 period_start 对应的节点汇总，将 quota 自动禁用恢复为 none；不改变 period_start、手动 enabled、expired 状态、过去账期或每日历史。

extra_json 必须为最多 256 KiB 的对象；禁止顶层 inbounds/experimental/log。outbounds 必须为最多 1000 个对象，含非空 type 和唯一 tag，tag 不可为受管的 direct。route 必须为对象，rules 若存在必须为对象数组，final 若存在必须是非空字符串。其它顶层项可存储；完整合并与 sing-box check 由第 13 步异步执行。

### 事务、审计与迁移

迁移 `0005_proxy.sql` 创建 inbounds、certs、node_core、config_revisions、node_advanced、subscribers、subscriber_assignments、subscriber_traffic、subscriber_traffic_daily。删除节点级联删除入站/证书/核心状态/修订/高级 JSON/分配，保留已计入用户额度的流量记录；删除用户才级联删除其流量。入站与用户 ID 使用 AUTOINCREMENT，不复用已删除 ID。

所有代理写入使用 BEGIN IMMEDIATE，业务修改和审计同事务；审计失败回滚业务。提交成功后才通知节点。审计动作包括 `inbound.create/update/delete/regenerate_keys`、`cert.generate/regenerate`、`node_advanced.update`、`subscriber.create/update/delete/reset_token/regenerate_credentials/reset_usage`、`assignment.update`。私钥、PSK、密码、用户凭据与 token 替换为 `***`；高级 JSON 仅记录大小、SHA256 与脱敏标记，避免任意扩展字段中的密钥进入审计。

## 9. 核心对齐、操作与统计（步骤 13）

以下接口均要求管理员 JWT，返回 no-store。核心详情沿用第 8 节 NodeCore 字段，并增加 `online`（当前 Agent 连接是否可用）、`current_version`（当前托管版本）、`pending`、`inbounds:[{name,up,down}]`（面板本次运行累计入站字节）。pending 表示最新目标修订与 Agent 已应用的修订号、配置哈希或安装版本不一致；running 和 online 是独立状态。

| 方法与路径 | 请求 / 响应 |
|---|---|
| `POST /api/servers/{id}/core/install` | 空体或 `{}`；202 `{queued:true,req_id}`，发送当前版本及对应架构 SHA256 |
| `POST /api/servers/{id}/core/restart` | 空体或 `{}`；202 `{queued:true,req_id}` |
| `POST /api/servers/{id}/core/apply` | 空体或 `{}`；200 `{revision,core}`，立即渲染并尝试重发；离线只保存目标 |
| `GET /api/servers/{id}/core/logs?lines=200` | lines=1–1000；200 `{kind:"error",text}`，最多 64 KiB；等待最多 10 s |
| `GET /api/servers/{id}/core/revisions` | 200 `{revisions:[{server_id,revision,sha256,created_at,created_by,version,ports,applied}]}`，最新在前，最多 20 条 |
| `GET /api/servers/{id}/core/revisions/{rev}` | 200 `{revision}`，另含完整 `config_json` 对象（含配置凭据，仅管理员可取） |
| `POST /api/servers/{id}/core/rollback/{rev}` | 空体或 `{}`；200 `{revision}`，创建新修订后尝试下发，响应省略 config_json |

apply 响应也省略 revision.config_json。安装/重启的 queued 只表示进入发送队列，执行结果通过 core.state 更新。参数/预检错误 400，未登录 401，对象/修订不存在 404；Agent 离线、并发核心操作、未选择当前版本或缺少托管资产 409；日志等待超时 504。无对齐器的测试/简化装配返回 503。GET revisions 不要求 Agent 在线。

变更后默认等待 5 s 合并同节点请求。渲染使用启用入站、已分配且 enabled=true/auto_disabled=none 的用户、节点证书和高级 JSON。SS2022 无用户时使用 managed=true 的空多用户 ACL；不能退回仅共享 PSK 的模式。统计地址固定 127.0.0.1:10085，入站占用 10085/tcp 时拒绝渲染。系统日志由 Agent 的 systemd unit 保存。

输出缩进 JSON，哈希计算前用 json.Compact；相同哈希与版本复用最新修订，当前核心版本改变则创建新修订。预检使用该版本的不可变托管文件，10 s 超时，临时配置 0600，诊断 stderr 不回传。Windows 不能运行 Linux 二进制，记 WARN 后交由 Agent 必须执行的 check；托管版本缺失仍报错。预检在写事务之外，提交前复核输入，旧快照不能覆盖其间提交的修改。

同节点只保留一个正在等待确认的 apply/install/restart。core.state 的 req_id 必须与当前请求匹配才可清除它，旧响应和普通周期状态不会替别的请求确认。首次 hello 等待紧随的新状态再对齐，周期性 hello 只刷新主机信息。版本不一致时等待管理员显式 install；不自动升级。apply 失败或 60 s 无响应产生 core.apply_failed 事件，并按 10/30/60 s 最多重试三次；新配置、手动 apply 或重连可重新尝试。当前目标已被实际状态确认后 pending=false。

回滚保存选中修订的配置、哈希和版本，以更高修订号下发，created_by=`rollback:{rev}`。它不回写入站/用户数据；后续配置数据变更或手动 apply 会再次按数据库渲染。启动与每分钟复核保留未改变源数据的回滚结果；恢复的面板数据库落后于 Agent 时，新修订号高于 Agent 已应用值。每次插入事务精确保留最近 20 条。

浏览器快照 `servers[].core={installed,running,version,users,pending,error}`；users 为最新目标配置中去重后的有效用户数。无核心服务装配时为 null。秒级广播使用缓存摘要，不逐节点查询数据库。

core.stats 不信任 Agent 时间，按面板接收日期归入 daily；用户只能来自该节点当前分配或仍在已应用修订中的 sub-ID。负数、超大计数、重复项拒绝，无法识别/其它节点用户忽略。每 60 s 事务写 subscriber_traffic/daily 并重算当前账期 traffic_used=up+down；累计值上限为 JS 安全整数 9007199254740991，避免整数溢出。并发新样本与失败批次合并重试，正常退出冲刷已接收样本；无持久消息队列或统计 ACK，强杀/断链仍有丢失或重复边界。

迁移 0006 为修订增加 version/ports，为 node_core 增加内部 input_sha256，为 subscribers 增加内部 usage_epoch。入桶记录当时账期和 epoch；reset-usage 在清表时递增 epoch，清零前的缓存不会在下次刷新时恢复用量。日期使用接收时的面板日期；更换账期前已捕获而未刷新的旧账期样本不计入新账期。

新增审计 `core.revision`、`core.apply`、`core.install`、`core.restart`、`core.rollback`，仅记录修订号/哈希/版本/请求 ID 等元数据；配置私钥和日志内容不写入审计。通知总线增加 `core.apply_failed`，后续步骤 19 可订阅。

## 10. 节点代理页的数据使用（步骤 14）

本步不新增 HTTP 接口。页面路由 `/dashboard/proxy` 与 `/dashboard/proxy/:serverId`，统一使用现有管理员 JWT API。

- 详情 core 每 5 s 刷新；快照 core/online 业务字段变化触发额外刷新。入站、证书、分配、打开的修订列表同样轮询；高级 JSON 只读状态。
- “分配用户”来自 `GET /api/subscribers` 的 `assigned_inbounds`，包含停用用户且节点内去重；与快照 `core.users`（目标配置中的有效用户）不同。
- `core.inbounds` 是面板本次运行的内存累计，重启后清零；UI 不把它标为账期用量。`core.listening` 是节点 /proc 中观察到的监听项，包含其它进程和回环端口，不能据此全部放行云安全组。
- 表单保存只发送可写 settings，省略 private_key/public_key/server_psk/obfs_password；首次 VLESS 留空 Short IDs 时省略该字段以自动生成，编辑态至少一项。密钥重生使用专用接口。
- 手动 apply 以响应中的 revision/sha256/version 为目标，观察在线、运行、pending=false 且三字段匹配后结束等待；UI 最多等待 60 s，不代替服务端的 req_id 确认机制。
- 日志显示/复制移除 ANSI 颜色序列，关闭抽屉取消 HTTP 等待；不会把主动取消显示为网络故障。所有密码与完整修订仅在既有管理员接口范围内读取。


## 节点账单与流量（步骤 18）

`GET /api/servers/{id}/traffic?months=12` 需要管理员 JWT，返回最新优先的数组 `[{period_start,period_end,in,out,used}]`；时间为 Unix 秒，当前账期 `period_end=null`，`months` 为 1–120，默认 12。读取前冲刷已接收的内存用量；历史 `used` 按节点当前 `traffic_mode` 计算。

REST 节点列表/详情增加 `traffic_used`，上限沿用 `traffic_limit`。WS 的 `traffic={used,limit,mode,in,out,period_start,period_end_expected}` 中 `limit=0` 为不限；`net.in_total/out_total` 改为当前账期累计，`net.up/down` 仍为实时速率。四种模式为 in/out/sum/max。

首次采集只建立基线；重复/旧时间戳忽略，单向计数变小视为该向归零，增量为当前值。计数器与账期总量每分钟同事务提交，退出时等待冲刷；重启加载持久基线。机器在未冲刷区间重启会丢失归零前无法恢复的量，不保证“只丢一秒”。累计统计跨过停机账期边界时只能归入恢复后的当前账期，未观测区间不按时间伪造拆分。

结算日按 VM_TZ，31 号在短月落到月末；启动和每分钟补齐遗漏的全部月界线，同时更新 `traffic.last_rollover_date`。改重置日保留当前用量，在修改后的下一个有效结算日关闭当前账期。零用量账期也保留。后台有一分钟调度精度，在线 metrics 可在首条跨界样本立即滚动。

自动顺延在启动补跑和每日 00:05 后执行：只处理 `expire_at < 今天 && auto_renew`，month/quarter/year 按日历加 1/3/12 月，短月夹到月末，多次补算直到晚于今天；once 不变。变更与 `actor=system, action=server.auto_renew` 审计同事务提交。提醒使用 `billing_reminders` 持久化每天/节点/阈值去重。

统一内存总线 Event 保留 Kind/ServerID/At，并增加 TargetType/TargetID/Threshold/Message。流量跨 80/90/100 发布 `server.traffic`，到期余 7/3/1 天发布 `server.expire`；节点 TargetType=server，TargetID=ServerID，Threshold 分别为百分比或天数，At 为面板时区时间。总线是非阻塞、非持久投递，不能视为通知成功回执。
## 步骤 15：公开订阅与用户流量

`GET /sub/{token}?format=clash|clash-provider` 无 JWT，未知 token 返回空 404；默认 clash。有效 token 每分钟 30 次，第 31 次 429 / Retry-After: 60。响应 no-store、text/yaml、profile-update-interval: 24 和 RFC 5987 文件名。subscription-userinfo 用 upload=0、download=traffic_used（计费累计量），不限额省略 total、无到期省略 expire；到期时间为面板时区该日结束。

用户手动或自动停用返回 200 空 proxies 和原因注释。仅收集已分配且启用的入站、现存且 public_host 非空的节点；同名加端口，跨同名节点再次冲突加 ID。模板 key sub.clash_template，缺省由 sub.DefaultClashTemplate 提供；含 PROXIES 和 PROXY_NAMES 占位，最终 YAML 必须为单文档且保持代理列表。

10 秒内存缓存仅保存渲染体，key 为 token 的 SHA-256 加格式；所有成功代理事务（用户、凭据、token、分配、入站、证书）以及设置和节点写入递增 DB.SubscriptionEpoch。调用直接 SQL 更新相关源的后续服务必须调用 DB.InvalidateSubscriptions。每次重新查 token/用户并生成用量头；变更期间完成的旧渲染不写入新 epoch 缓存。

`GET /api/subscribers/{id}/traffic` 需 JWT，返回 by_server:[{server_id,name,up,down}]（当前 period_start，删除节点保留原用量与 ID）、daily:[{date,up,down}]（面板日期最近 30 天，缺日补零）。方向量是原始上下行数据，不保证与切换计费模式后的累计 traffic_used 相等。
