# 步骤 05 · 实时 Hub

> 阶段 1 · 依赖：03、04 · 粗估 2–3 天 · 对应设计方案 §6.2、§6.3、§7.1–§7.3

## 1. 目标

agent 能连上服务端并被认出；服务端维护每台节点的最新状态与在线判定；浏览器能通过 WS 拿到每秒一帧的全量快照。

## 2. 范围

**做**：Agent Hub（鉴权、单连接、消息分发框架、host info 落库、最新状态）、离线扫描、Client Hub（JWT 鉴权、快照广播、压缩）、`GET /api/servers` 合并在线状态、`config` 下发。

**不做**：指标落库（08）、ping/core 消息的业务处理（09/13，这里只留分发挂点）。

## 3. 前置条件

步骤 03（servers 表、token）、04（agent 能发 hello/metrics）。

## 4. 实现方案

### 4.1 状态 `server/internal/hub/state.go`

```go
type ServerState struct {
    ID int64
    Online bool; LastSeen time.Time; PublicIP string; AgentVersion string
    Host proto.HostInfo
    Latest *proto.Metrics           // 最后一条
    // 后续步骤追加：PingRecent、Core、Traffic
}
type Registry struct { mu sync.RWMutex; m map[int64]*ServerState }
func (r *Registry) Get(id) *ServerState / Update(id, func(*ServerState)) / Snapshot() []ServerView
```

`ServerView` 是对外快照结构（设计 §7.3），由 `Registry.Snapshot()` 结合 `store.servers` 的静态配置（名称、地区、标签、到期等，带 60 秒缓存）组装。

### 4.2 Agent Hub `hub/agent.go`

- 路由 `GET /api/agent/ws`（不走 JWT 中间件）：
  1. 读 `Authorization: Bearer` → sha256 → `store.FindByTokenHash`，失败 401
  2. `gorilla/websocket.Upgrader{EnableCompression: true, CheckOrigin: 放行}`
  3. 同一 `server_id` 已有连接 → 先关旧连接（`CloseMessage` 原因 `superseded`）
  4. 记录 `PublicIP = RealIP`，标记 Online，写日志
  5. 发送 `config`（`report_interval: 1`，`ping_tasks` 暂空数组）
- 读循环：`SetReadDeadline(now+30s)`，每条消息先解 `Envelope.Type`，查 `handlers map[string]Handler` 分发；未知类型记 warn 丢弃。本步注册：
  - `hello`：更新 `Host`、`AgentVersion`，`store.hostinfo.Upsert`
  - `metrics`：更新 `Latest`、`LastSeen`；调用 `onMetrics` 钩子链（步骤 08 聚合、步骤 18 流量结算在此挂载）
- 写：每连接一个 `chan []byte`（容量 64）+ 写协程，满则断开该连接；`SendTo(serverID, msg)` 供后续步骤下发 `config`、`core.apply`。
- 断开：Online 置 false 不立即广播（交给扫描器统一判定，避免重连抖动），清理连接表。

### 4.3 离线扫描 `hub/offline.go`

每 5 秒遍历 Registry：`Online && now-LastSeen > 15s` → `Online=false`，向 `events` 总线发 `ServerOffline{id, since}`；重新收到 metrics → `Online=true` 并发 `ServerOnline`。总线是一个简单的 `chan Event` + 订阅者列表（步骤 19 告警订阅）。

### 4.4 Client Hub `hub/client.go`

- 路由 `GET /api/ws`（不走 JWT 中间件，自己在首帧校验）：升级后等第一条消息 `{type:"auth", token}`，5 秒未到或校验失败 → 关闭 `4001`。
- 连接表 `map[*conn]struct{}`；`Broadcaster` 每秒：`views := registry.Snapshot()` → 一次 `json.Marshal` → 逐连接 `WriteMessage`（`SetWriteDeadline 1s`，失败即移除）。
- `permessage-deflate`：`EnableCompression: true` + `conn.EnableWriteCompression(true)`。
- 断开即移除；没有客户端时广播协程空转（每秒检查一次即可）。

### 4.5 REST 合并

`GET /api/servers` 返回配置 + `online/last_seen/public_ip/host/agent_version`；`GET /api/servers/{id}` 同。

### 4.6 快照结构（本步字段）

```json
{"type":"snapshot","ts":1757660000,"servers":[
  {"id":1,"name":"深圳-阿里云-01","region":"CN","group":"","tags":[],"sort":0,
   "online":true,"last_seen":1757660000,"v4":true,"v6":false,
   "cpu":3.02,"cores":2,"mem":{"used":458000000,"total":1690000000},"swap":{"used":0},
   "disk":{"used":9720000000,"total":42000000000},"load":[0.04,0.03,0.0],
   "net":{"up":303,"down":169,"out_total":126900000,"in_total":1557000000},
   "conn":{"tcp":23,"udp":4},"procs":112,"uptime":172800,
   "expire_at":"2027-09-10","bandwidth":"3Mbps","price":99,"currency":"CNY","cycle":"year",
   "traffic":null,"ping":[],"core":null}
]}
```

`traffic`、`ping`、`core` 三个字段先占位 null/空，后续步骤填充；前端按可空处理。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| WS | `GET /api/agent/ws`（agent，Bearer token）；`GET /api/ws`（浏览器，首帧 `auth`） |
| 消息 | server → 浏览器 `snapshot`；server → agent `config` |
| REST | `GET /api/servers`、`GET /api/servers/{id}` 增加在线字段 |

## 6. 验收标准

- [ ] 用步骤 03 创建的 token 启动 agent，日志显示 `agent connected`，`GET /api/servers` 里该节点 `online=true`、`host` 已填
- [ ] 错误 token → 401，agent 按退避重连
- [ ] 同一 token 起两个 agent，旧连接被踢，服务端只保留一条
- [ ] `kill` agent 后 15–20 秒内 `online=false`；重启 agent 后立即恢复
- [ ] 用 `websocat wss://…/api/ws` 发 `{"type":"auth","token":"…"}` 后每秒收到一帧 snapshot；不发 auth 5 秒后被断开
- [ ] 两个浏览器客户端同时连接，服务端每秒只序列化一次（日志或计数器确认）
- [ ] 十几台节点时单帧小于 15 KB（压缩前），CPU 占用不可见

## 7. 风险与注意

- gorilla 的 `EnableCompression` 需要客户端也协商；浏览器默认支持，agent 用 coder/websocket 也支持。
- Registry 的 `Snapshot()` 每秒调用，静态配置（名称等）不要每次查库，60 秒缓存 + 写接口主动失效。
- 时间戳统一用服务端接收时间判在线，不用 agent 上报的 `ts`（时钟可能不准）。

## 8. 产出物

`server/internal/hub/{state,agent,client,offline,events}.go`、`api/servers.go` 合并在线字段、`protocol.md`（snapshot、auth 定稿）。

## 9. 偏离记录

> 以下三条是 2026-09-12 开工准备时定的，代码已落地（提交见该日 `chore(step-05)`），主体实现尚未开始。

1. **WebSocket 库用 `coder/websocket`，不用本文 §4.2 / §7 写的 `gorilla/websocket`。** 步骤 04 的偏离记录第 4 条已经定了两端统一用 `coder/websocket`，agent 侧就是这么实现的；服务端跟着走，避免一个项目里两套 WS 库。`server/go.mod` 已加 `github.com/coder/websocket v1.8.15`，并删掉零引用的 `github.com/gorilla/websocket v1.5.3 // indirect`（那是步骤 02 预留的占位）。写 `hub/agent.go` 时的 API 对应关系：`Upgrader{}` → `websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover, ...})`；踢旧连接的 `CloseMessage` → `conn.Close(code, reason)`；§7 提到的 `EnableCompression` 协商在 coder 这边由 `CompressionMode` 控制，agent 侧已经在用（`transport/client.go`）。

2. **`server/go.mod` 补上 `require vpsmon/proto v0.0.0`。** 此前只有 `replace vpsmon/proto => ../proto` 而没有 require 行（步骤 02 拆分 require 块时丢的），而服务端代码至今一次都没 import 过 proto——hub 一 import 就会编译失败。顺带跑了 `go work sync`，它把 agent 的 `golang.org/x/sys` 从 v0.46.0 提到 v0.48.0 与 server 对齐，并把 `stretchr/testify` 显式化为 indirect。**注意这是依赖版本的实际变更**：工作区构建本来就按 MVS 取高版本解析，`go work sync` 只是让 `agent/go.mod` 与实际构建结果一致，不再出现"模块声明 v0.46.0、工作区里编的是 v0.48.0"的错位。三模块 `go vet` / `go test` / 两架构交叉编译已复验通过。

3. **内嵌前端的占位机制改掉了（覆盖步骤 01 偏离记录第 9 条）。** 原做法是把占位 `index.html` 提交进 `server/web/dist/`（`.gitignore` 用 `!/server/web/dist/index.html` 反选）。问题是 `make build-web` 产出的真入口和这个被跟踪的占位文件是同一个路径，**任何一次 `git checkout` 都会把真入口静默盖回占位页**，而 `git status` 干净、`go build` 成功，故障只在浏览器里才暴露——2026-09-12 核验时仓库正处于这个状态（assets 是真产物、index.html 是占位页）。新做法：

   - `server/web/dist/` 只跟踪一个 `.gitkeep`，给 `//go:embed all:dist` 留个匹配目标（`all:` 前缀会匹配点开头的文件，实测只有 `.gitkeep` 时编译通过）；构建产物一律不进版本库
   - 占位页移到 `server/web/placeholder.html` 单独 `//go:embed`，`handlerFor` 读不到 `dist/index.html` 时回退到它（原来是返回 404 `frontend not built`，现在保持步骤 01 那个中文提示页的行为）
   - `Makefile` 的 `build-web` 在 `rm -rf` 重建 dist 后补回 `.gitkeep`

   两种状态都实测过：dist 只有 `.gitkeep` 时（全新 clone）编译通过、SPA 路由下发 394 字节占位页；跑完前端构建后下发 756 字节真入口，且引用的 `assets/index-*.js` 返回 200。
