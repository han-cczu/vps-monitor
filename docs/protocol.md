# 协议与接口

> 本文件随代码走：每个步骤新增或改动的 WebSocket 消息、REST 接口都记到这里。
> 设计方案（`设计方案.md`）不改，改了就记到对应步骤文档的"偏离记录"。

## 1. WebSocket · agent ↔ server

端点：`wss://{面板域名}/api/agent/ws`

（步骤 04 起填充。消息结构体见 `proto/msg.go`。）

### 信封

```json
{ "type": "metrics", "id": "可选，请求响应配对用", "ts": 1757600000000, "data": {} }
```

| 方向 | type | 说明 | 引入步骤 |
|---|---|---|---|
| — | — | — | — |

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
- 访问日志只记路径，不记 query。

### 3.2 接口

| 方法 | 路径 | 鉴权 | 说明 | 引入步骤 |
|---|---|---|---|---|
| GET | `/api/health` | 无 | `{ "ok": true, "version": "dev" }` | 01 |
| POST | `/api/auth/sign-in` | 无 | 登录。body `{ username, password }`（也接受 starter 的 `email` 字段名作为用户名）。200 `{ accessToken, expiresAt, user }`；401 `{ "message": "用户名或密码错误" }`；429 `{ "message": "尝试次数过多，请 N 分钟后再试" }` + `Retry-After` | 02 |
| GET | `/api/auth/me` | JWT | 当前用户 `{ user }` | 02 |
| POST | `/api/auth/password` | JWT | 改密码。body `{ oldPassword, newPassword }`：新密码 ≥ 10 个字符、≤ 256 字节、不能与旧密码相同。成功 204；当前密码不对 400 `{ "message": "当前密码错误" }`（不用 401，避免前端把会话清掉）；旧密码连错 5 次后 429 | 02 |

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

## 4. 数据表

迁移文件在 `server/internal/store/migrations/`，goose 格式，只增不改。所有时间都是 Unix 秒。

### `0001_init.sql`（步骤 02）

| 表 | 字段 | 说明 |
|---|---|---|
| `users` | `id`, `username` UNIQUE, `password_hash`, `totp_secret`, `totp_enabled`, `created_at` | 密码是 argon2id PHC 串 `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`；TOTP 两列步骤 20 才用 |
| `settings` | `key` PK, `value` | `value` 是 JSON |
| `audit_log` | `id`, `ts`, `actor`, `action`, `target_type`, `target_id`, `before`, `after`, `ip` | `actor` 是用户名或 `system`；`before` / `after` 是 JSON；索引 `idx_audit_ts` |

### 审计动作

| action | target_type | 引入步骤 | 说明 |
|---|---|---|---|
| `auth.sign_in` | `user` | 02 | 登录成功 |
| `auth.password_change` | `user` | 02 | 改密码成功（不记录密码哈希） |
