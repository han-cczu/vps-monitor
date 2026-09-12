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

## 2. 构建

```sh
make build-web      # 前端构建产物拷到 server/web/dist
make build-server   # 内嵌前端，产出 dist/server/vps-server
make build-agent    # 产出 dist/agent/vps-agent-linux-{amd64,arm64}
```

## 3. 部署

（步骤 07 填充。届时记得把 `VM_TRUSTED_PROXIES` 一起写进 compose 的环境变量。）

## 4. 备份与恢复

（步骤 07 填充。目前需要保住的只有 `{VM_DATA_DIR}/vm.db` 与 `jwt.secret`。）

## 5. 排障

### 忘记管理员密码

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

### 看访问日志

每个请求一行 JSON，字段：`method`、`path`、`status`、`bytes`、`dur_ms`、`ip`、`req_id`。`/api/*` 记 `INFO`；静态资源与 `/api/health` 记 `DEBUG`，要看得 `VM_LOG_LEVEL=debug`。日志里不含 query 参数。

服务端 panic 会记一条 `panic recovered` 的 `ERROR`，带 `stack` 字段（完整堆栈）和 `req_id`，同时给客户端返回 500 `{"message":"服务器内部错误"}`。
