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

- [x] 用步骤 03 创建的 token 启动 agent，日志显示 `agent connected`，`GET /api/servers` 里该节点 `online=true`、`host` 已填
- [x] 错误 token → 401，agent 按退避重连
- [x] 同一 token 起两个 agent，旧连接被踢，服务端只保留一条
- [x] `kill` agent 后 15–20 秒内 `online=false`；重启 agent 后立即恢复
- [x] 用 `websocat wss://…/api/ws` 发 `{"type":"auth","token":"…"}` 后每秒收到一帧 snapshot；不发 auth 5 秒后被断开——**用自写的小客户端代替 websocat**，见偏离记录 10
- [x] 两个浏览器客户端同时连接，服务端每秒只序列化一次（日志或计数器确认）——用 `ClientHub.Frames()` 计数器 + 单测确认，见偏离记录 10
- [x] 十几台节点时单帧小于 15 KB（压缩前），CPU 占用不可见

## 7. 风险与注意

- gorilla 的 `EnableCompression` 需要客户端也协商；浏览器默认支持，agent 用 coder/websocket 也支持。
- Registry 的 `Snapshot()` 每秒调用，静态配置（名称等）不要每次查库，60 秒缓存 + 写接口主动失效。
- 时间戳统一用服务端接收时间判在线，不用 agent 上报的 `ts`（时钟可能不准）。

## 8. 产出物

`server/internal/hub/{state,agent,client,offline,events}.go`、`api/servers.go` 合并在线字段、`protocol.md`（snapshot、auth 定稿）。

## 9. 偏离记录

> 以下三条是 2026-09-12 开工准备时定的，代码已落地（提交见该日 `chore(step-05)`），主体实现尚未开始。

1. **WebSocket 库用 `coder/websocket`，不用本文 §4.2 / §7 写的 `gorilla/websocket`。** 步骤 04 的偏离记录第 4 条已经定了两端统一用 `coder/websocket`，agent 侧就是这么实现的；服务端跟着走，避免一个项目里两套 WS 库。`server/go.mod` 加了 `github.com/coder/websocket v1.8.15`，并删掉零引用的 `github.com/gorilla/websocket v1.5.3 // indirect`（那是步骤 02 预留的占位）。
   > **更正（实现后审查发现）**：开工准备那次提交里，这条 require 实际上没留住——当时 server 还没有任何代码 import 它，`go work sync` 把它当作多余依赖剪掉了，提交 diff 里只剩"删掉 gorilla"。直到 hub 真的 import 之后重新补上才生效。教训：**在没有 import 的情况下预置依赖，`go work sync` 会把它删掉**（步骤 01 偏离记录第 8 条警告过"别 tidy"，`go work sync` 是同一类操作）。写 `hub/agent.go` 时的 API 对应关系：`Upgrader{}` → `websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover, ...})`；踢旧连接的 `CloseMessage` → `conn.Close(code, reason)`；§7 提到的 `EnableCompression` 协商在 coder 这边由 `CompressionMode` 控制，agent 侧已经在用（`transport/client.go`）。

2. **`server/go.mod` 补上 `require vpsmon/proto v0.0.0`。** 此前只有 `replace vpsmon/proto => ../proto` 而没有 require 行（步骤 02 拆分 require 块时丢的），而服务端代码至今一次都没 import 过 proto——hub 一 import 就会编译失败。顺带跑了 `go work sync`，它把 agent 的 `golang.org/x/sys` 从 v0.46.0 提到 v0.48.0 与 server 对齐，并把 `stretchr/testify` 显式化为 indirect。**注意这是依赖版本的实际变更**：工作区构建本来就按 MVS 取高版本解析，`go work sync` 只是让 `agent/go.mod` 与实际构建结果一致，不再出现"模块声明 v0.46.0、工作区里编的是 v0.48.0"的错位。三模块 `go vet` / `go test` / 两架构交叉编译已复验通过。
   > **更正（实现后审查发现）**：同上，`require vpsmon/proto` 也被 `go work sync` 剪掉了，直到 hub import 了 `proto` 才补回来。后果是当时 `cd server && GOWORK=off go build ./...` 编不过——而步骤 07 单独构建 server 镜像时就是这个环境。现在已经把"脱离 go.work 能否编译"纳入本步的复验项。

3. **内嵌前端的占位机制改掉了（覆盖步骤 01 偏离记录第 9 条）。** 原做法是把占位 `index.html` 提交进 `server/web/dist/`（`.gitignore` 用 `!/server/web/dist/index.html` 反选）。问题是 `make build-web` 产出的真入口和这个被跟踪的占位文件是同一个路径，**任何一次 `git checkout` 都会把真入口静默盖回占位页**，而 `git status` 干净、`go build` 成功，故障只在浏览器里才暴露——2026-09-12 核验时仓库正处于这个状态（assets 是真产物、index.html 是占位页）。新做法：

   - `server/web/dist/` 只跟踪一个 `.gitkeep`，给 `//go:embed all:dist` 留个匹配目标（`all:` 前缀会匹配点开头的文件，实测只有 `.gitkeep` 时编译通过）；构建产物一律不进版本库
   - 占位页移到 `server/web/placeholder.html` 单独 `//go:embed`，`handlerFor` 读不到 `dist/index.html` 时回退到它（原来是返回 404 `frontend not built`，现在保持步骤 01 那个中文提示页的行为）
   - `Makefile` 的 `build-web` 在 `rm -rf` 重建 dist 后补回 `.gitkeep`

   两种状态都实测过：dist 只有 `.gitkeep` 时（全新 clone）编译通过、SPA 路由下发 394 字节占位页；跑完前端构建后下发 756 字节真入口，且引用的 `assets/index-*.js` 返回 200。


---

> 以下是主体实现时的偏离，2026-09-12。

4. **`Registry.Get` 返回状态副本，不是 §4.1 写的 `*ServerState`。** 指针一旦逃出读锁，调用方读 `Latest` 的同时 agent 协程可能正在写，就是一个数据竞争。`Latest` 指向的 `proto.Metrics` 只整体替换、不原地改，所以随副本共享是安全的。

5. **gorilla 的三处 API 在 coder/websocket 里没有直接对应，都换了写法**（承接偏离 1）：
   - §4.2 的 `SetReadDeadline(now+30s)`：coder 没有这个方法，改成每次 `Read` 套一个 30 秒的 context 超时。**注意语义差别**：coder 的 `Read` 不会因为收到 ping 而返回，所以这个超时计的是"数据帧沉默"时长，而不是"链路沉默"。连上就下发 `report_interval: 1`，正常每秒都有 metrics，30 秒足够宽。
   - §4.4 的 `EnableCompression` + `EnableWriteCompression(true)`：在 coder 里是 `AcceptOptions.CompressionMode: CompressionContextTakeover`，一个选项管两边。
   - 踢旧连接的 `CloseMessage`：换成 `conn.Close(websocket.StatusNormalClosure, "superseded")`。

6. **浏览器首帧鉴权的超时不能用 `context.WithTimeout` 套 `Read`——这是实测踩出来的。** coder/websocket 在 `Read` 的 ctx 过期时会直接把连接拆掉，于是"5 秒没发 auth"的对端只看到一个 EOF，收不到设计要求的关闭码 4001。改成自己起 timer 计时：超时时连接还活着，4001 能正常发出去。实测对端收到的就是 `StatusCode(4001)`，耗时 5.0 秒。

7. **踢连接的 `Close` 放进单独协程——同样是实测踩出来的。** coder 的 `Close` 会等对端回一个 close 帧（最长几秒）。原先在新连接的握手路径里同步调用，结果新 agent 迟迟收不到 `config`（测试里直接超时）。现在 `close()` 里起一个协程做「发 close 帧 → 等回帧 → cancel」，调用方不阻塞。`cancel` 也必须等 `Close` 返回再调：提前取消会让 handler 立刻退出并 `CloseNow`，把还没发出去的 close 帧连同 socket 一起拆掉。

8. **`Disconnect` 先摘连接表再关闭。** 关闭是异步的（见上一条），这期间不能再让 `SendTo` / `Connected` 把一条已经作废的连接当成可用的——重置 token 的场景尤其不能。

9. **`snapshot` / `auth` 的结构放在 `hub` 包，没有进 `proto`。** `proto` 是 agent 与 server 共享的零依赖包，而 `ServerView` 里带着 `price` / `expire_at` / `cycle` 这些只属于面板的字段，agent 不该知道；而且快照不是 `proto.Metrics` 的复用而是**投影**（字段刻意改短、重新分组、合进库里的配置），本来就要单独写一遍。第二个消费者是浏览器（TypeScript），享受不到 Go 类型共享的好处。
   代价是 Go 与 TS 之间没有编译器对字段名，所以补了 `testdata/snapshot.json` 的 golden 测试：它是照着 §4.6 手写的，不是从代码 dump 的，改字段名会先让测试红，而不是等步骤 06 在浏览器里发现卡片空白。

10. **验收方式的两处替代**：
    - 第 5 条要求的 `websocat` 本机没有，改用 `coder/websocket` 写了个几十行的小客户端跑（放在 scratchpad，未入库）。验的内容不变：auth 后连续三帧 `snapshot`、间隔 1.00 秒；不发 auth 在 5.0 秒后收到关闭码 4001。
    - 第 6 条的"每秒只序列化一次"用 `ClientHub.Frames()` 计数器 + 单测 `TestBroadcastSerializesOnceForTwoClients` 确认，没有靠肉眼看日志。

11. **REST 侧顺手修掉步骤 03 留下的两处不一致**（都在本步的接线范围内）：
    - `PUT /api/servers/{id}` 之前把 `host` 写死成 `null`，和 `GET` 的返回体对不上，而前端按同一个类型用这个返回值——现在会把 `host` 与在线状态一起查出来返回。`POST` 仍然是 `false` / `null`，那是事实（刚建的节点没连过），不是占位。
    - `POST /api/servers/{id}/token` 现在会把该节点在线的 agent **当场断开**。鉴权只在握手时做一次，不踢的话拿着已作废 token 的 agent 能一直连到自己断开为止。

12. **几个文档没写死、实现时定下来的细节**：
    - 没有浏览器客户端连着时，广播协程完全跳过——不查库、不序列化（`TestBroadcastSkipsWorkWithoutClients` 守着）。
    - 浏览器连上后立刻推一帧，不等下一个整秒，打开页面就有数据。
    - 掉线的节点在快照里保留最后一次的数值（不归零），前端置灰显示即可。
    - 事件总线 `Subscribe` 不支持取消（现有订阅者都与进程同生命周期），`Publish` 非阻塞：订阅者缓冲满了就丢，绝不让慢订阅者卡住离线扫描。
    - `api.Deps.Hub` 为 nil 时两个 WS 端点不注册、`online` 恒 `false`。api 包的既有单测因此一行没改。

13. **两个 agent 共用同一个 token 会互相 ping-pong**（实测观察到）。每条连接建立即被对方踢掉，因为活不过 30 秒，退避会一路涨到 60 秒封顶，不会打满 CPU。验收第 3 条"服务端只保留一条"始终成立。已写进 `protocol.md` §1.7 与 `runbook.md` 的排障表。

## 10. 验收情况

2026-09-12 全部在本机跑通，用的是真实编译产物（`server/cmd/server` 与 `agent/cmd/agent`），不是 mock：

- **agent 接入**：真 agent 连上真服务端（走完整路由栈，含 `middleware.Compress`），服务端日志 `agent connected server_id=1 public_ip=127.0.0.1 agent_version=dev`；`GET /api/servers` 返回 `online=true`、`last_seen` 有值、`host` 填齐（hostname / os / arch / cores=20 / mem_total / public_ip / agent_version / ipv4 / ipv6）。
  > 这条同时验掉了开工前最大的一个未知：**chi 的 `middleware.Compress(5)` 不会破坏 WebSocket 升级**。
- **错误 token**：agent 日志 `服务端拒绝了 token（401）`，退避 1s → 2s → 4s 递增。
- **踢旧连接**：第二个 agent 连上后，服务端打出「踢掉同一节点的旧连接」，第一个 agent 收到 `status = StatusNormalClosure and reason = "superseded"` 并重连。
- **掉线判定**：`kill` agent 后 **16.1 秒**判为离线（扫描周期 5 秒 + 超时 15 秒，落在验收要求的 15–20 秒内）；重启 agent 后立刻恢复 `online=true`。
- **浏览器接入**：发 `auth` 后连续收到三帧 `snapshot`，`ts` 逐秒递增、间隔 1.00 秒，实时字段在动；不发 `auth` 在 5.0 秒后被以 **4001** 关闭。
- **帧大小**：15 台节点的真实快照 **7994 字节**（其中 1 台有实时数据）；单测里 15 台全部填满实时数据是 **8849 字节**，都远低于 15 KB。
- **PUT 返回体**：改名后返回的 `server` 带上了 `online=true` 与 `host.hostname`，与 `GET` 一致。
- **自动化**：`go vet` 三模块退出 0；hub 包新增 20 个测试全过，server 模块共 82 个测试全过；`tsc:check` 与 `lint` 未受影响。

**没能验的一条**：`go test -race` 在本机仍然跑不起来（`-race requires cgo`，PATH 里没有 gcc）。hub 是本项目第一块真正的并发代码（Registry 读写、每秒广播、连接进出），却没被竞态检测器扫过——这是**本步最大的已知风险**，建议在 CI 的 go job 里加 `-race`（注意：步骤 04 的 transport 测试自身带一处数据竞争，一开 `-race` 会先红，要一并处理）。

## 11. 实现后审查（2026-09-12/13）

主体写完后跑了一轮多 agent 对抗性审查：4 个维度（并发正确性、协议与对端契约、健壮性与资源、接线影响面）各出一份问题清单，每条再派一个独立 agent **专门去推翻它**。共提出 32 条，推翻 15 条，确认 17 条（去重后 10 个独立问题）。之所以做这一轮，是因为本机没有 gcc、`go test -race` 跑不起来，而 hub 是本项目第一块真正的并发代码。

已修的（都补了回归测试）：

1. **配置缓存被一次查库失败"毒化" 60 秒**（最严重的一条）。原实现把错误连同新鲜时间戳一起写进缓存，于是一次瞬时失败会让**所有浏览器**的节点卡片整体消失一分钟，期间还不重试，而同文件的注释写的恰恰是"库读不出来时返回上一次缓存"——代码和自己的契约打架。审查用 `go test -overlay` 在真实代码上复现了：库第 2 毫秒就恢复，快照却连续 59 帧是 `servers:[]`。
   改：查库失败不覆盖缓存、不刷新时间戳（下次调用立刻重试）；`InvalidateConfig` 也只清时间戳、保留数据当兜底。回归测试 `TestConfigCacheKeepsStaleDataOnError`。

2. **浏览器鉴权失败时同步 `conn.Close` 把 handler 钉住 10 秒**。首帧超时后那个读协程还占着 coder 内部的 `readMu`，而 `Close` 要抢同一把锁等对端回帧（硬编码 5 秒），实测 handler 从 Accept 到返回占用 10.0 秒。`/api/ws` 在鉴权之前就完成升级、没有任何连接数限制，这条路径对谁都敞开。
   改：失败收尾整个丢给协程，handler 立刻返回；读协程也从请求 ctx 上摘下来（handler 返回时 net/http 会取消它，同样会把还没发出去的 4001 拆掉）。

3. **`server/go.mod` 的两条 require 根本没留住**，见 §9 第 1、2 条的更正。现在 `GOWORK=off go build ./...` 与 `GOWORK=off go test ./...` 都通过。

4. **metrics 指针同时进 Registry 和外部钩子**。`Get` 返回的状态副本与 Registry 共享 `Latest` 指针，安全性建立在"只替换不原地改"上，而钩子（步骤 08 聚合、18 结算）拿到的是同一个指针，没有任何东西拦着它原地改。改：每个钩子拿自己的值副本。

5. **删除节点后在途 metrics 会复活内存态**。`Remove` 是同步的、`Disconnect` 是异步的，中间在途的消息会通过"按需新建"把记录重新建出来，而它已经不在配置表里——快照看不见它，只会一直占着内存。改：agent 消息路径改用 `UpdateExisting`，不隐式新建；丢弃时记 WARN。

6. **服务端重启后快照把 `cores` / `mem.total` / `disk.total` / `v4` / `v6` 显示成 0**，而 REST 同时返回着库里的真值——同一份数据两个接口对不上。改：启动时用 `server_host_info` 预热内存态。实测重启后 agent 未重连时快照是 `online=false` 但 `cores=20`、`mem.total` 有值。

7. **坏 agent 能用 ~1 KB 上行换服务端 ~1 MB 日志**：未知消息类型、`error` 消息的 `op` / `message` 都是对端可控字符串，原样进日志，而单帧上限 1 MiB、每秒可发一条。改：写日志前一律截断到 200 字节。

8. **每条 WebSocket 长连接白占一个 gzip 编码器**。chi 的 `Compress` 中间件按请求从 `sync.Pool` 取编码器，而 WS 升级后连接被 Hijack 走、一直活到断开，编码器也就被占到那时候；101 响应本身根本没有可压缩的 body。改：加一层 `skipUpgrades`，升级请求直接跳过压缩中间件（顺带修了 `recoverer` 里 `Connection == "Upgrade"` 的判断——它漏了 `keep-alive, Upgrade` 这种列表形式）。

9. **两个 WS 路由"不在 JWT 组内"这条接线约束零测试覆盖**。审查做了变异测试：把路由挪进 protected 组，全部测试仍然是绿的，而两个端点都会在握手阶段变成 401。改：加 `api/ws_test.go`，用"有没有走到 hub"当判据（走到了会因为缺 Upgrade 头得到 426，没走到是中间件的 401）。我自己也跑了一遍同样的变异，确认新测试会红。

10. **三处代码与文档不符**，都已对齐 `protocol.md`：握手成功就标记在线并置 `last_seen`（不是"收到 metrics 才在线"）；`proto_version` 只记 WARN、不做"兼容判断"（协议只有一版，无从判起）；预热后静态字段不再是 0。

没修、留作后续的：

- **`/api/ws` 在鉴权之前就完成升级，未鉴权连接数没有上限**。修掉第 2 条之后单条连接的成本已经从 10 秒降到接近 0，但"升级先于鉴权"这个结构还在。真要收口需要一个握手期并发上限，属于步骤 20「安全与运维打磨」的范畴，这里只记录。
- **`go test -race` 仍然没跑过**（本机无 gcc）。本轮审查是对它的替代，不是等价物。建议在 CI 的 go job 加 `-race`；注意步骤 04 的 transport 测试自身带一处数据竞争，一开 `-race` 会先红，要一并处理。

被推翻的 15 条里，比较值得记一笔的几条：「服务端 30 秒读超时正好等于 agent 的 30 秒稳定连接阈值会导致抖动」（复核发现 agent 每秒上报，根本走不到读超时）、「广播协程里同步 `close()` 会让全体停播 5 秒」（复核发现 `trySend` 的 default 分支只在队列满时触发，而 `close()` 里的 `Close` 是异步的）、「关停时 WS 连接没有收尾」（复核确认 hijack 的连接本来就不归 `srv.Shutdown` 管，进程退出即可）。
