# 外部代理观测协议

`proto` 保持标准库零依赖。原有严格解码的 `core.state/core.stats` 不扩展字段。

## 协商和消息

1. Agent hello 中声明 `capabilities: ["proxy.observe.v1"]`，并上报 `proxy_management`：`managed`（所有权验证通过）、`none`（无资源冲突，可安装）、`external`（第三方或冲突）、`unknown`（尚无法确认）。
2. 服务端仅对支持能力的当前认证连接，在 `config.proxy_observe_session` 下发随机 128 位 session。每次重连重新生成；后续 ping/config 更新保留它。面板清理观测记录时也会换一个（见「清理与在途上报」）。
3. 收到非空 session 后 Agent 才发送 `proxy.observation`，包含 `session,sequence,page,pages,scan_complete,collected_at,instances`。`page` 从 0 开始；每帧最多 1 个实例片段，每片段最多 64 个入站。新 session 的 sequence 重新递增。
4. `proxy.refresh` 无参数，只请求本地观察器重新采集；不得带路径、shell、SQL 或统计清零参数。老服务端不确认能力时，新 Agent 不发送观测消息，但所有权保护始终生效。

实例数据白名单见 `proto/proxyobserve.go`：来源、核心类型、版本、运行 PID/启动 tick、管理器 PID、service/binary/config_paths、网络命名空间、RSS、PID 对应绑定端口、入站摘要、统计来源和完整性。入站只保留类型/标签/监听地址端口/传输/TLS/Reality/用户数量/启用状态；不保留凭据、原始 JSON、用户名称、密钥或数据库内容。UUID 形式及超长令牌形式的标签会隐藏。

上限：256 KiB/帧、128 页/次、32 个当前实例、1024 入站/实例、4096 入站/次、4 MiB/组装。发现超过限制显式标记；服务端拒绝超限、乱序、重放、超时 30 秒的组装和未知字段。整个快照完整后才在事务中提交。缺失页不会覆盖已有数据。服务端 server_id 来自连接身份，不接受 Agent 指定其它节点。

## 保存与用量语义

`0015_proxy_observation.sql` 新增独立 `proxy_observations` 表，以 `(server_id,instance_id)` 保存完整白名单快照、接收时间和 absent 标记；`0017_proxy_observation_history.sql` 增加节点级扫描状态 `proxy_observation_scans` 和历史记录的 `absent_at`。完整发现中未出现的实例保留最后快照并标记 absent，同时记下确认消失的时刻；不完整发现不会将缺失实例判定为消失，也不会把它算作一次成功采集。当前实例与历史记录在接口里一起返回、在页面上分开显示：absent 的记录立即退出当前列表并进入默认折叠的历史区。

- 每节点的扫描状态记录最近一次提交是完整还是不完整、采集与接收时间、那一轮发现几个实例。只有「在线 + 能力已协商 + 完整 + 未过期」的完整扫描才能得出“当前未发现代理实例”；等待首个快照、采集不完整、结果过期都只能说“无法确认”。
- 离线、absent 或超过 90 秒未更新的结果标为 stale。stale 只说明数据不是刚收到的，不代表实例已经消失。
- 新鲜度按实例判断，不能只看节点级的 `scan.stale`：扫描状态每提交一轮就更新，而某一轮没有上报的实例其 `received_at` 停留在上一次。节点的 `scan.received_at` 比某条记录的 `received_at` 新时（例如连续几轮不完整扫描只报出 B，A 已经很久没出现），这条记录的进程、端口绑定与入站都按历史读数展示，页面在卡片上直接标出“不在最近一次采集里”。配置摘要与入站说明它们来自哪一次成功读取；用量是每轮单独采集的，读不到或已被清空时明确显示不可用，不沿用旧值。
- 卡片状态同样按实例判断：节点不再确认这一条（离线，或它不在最近一轮里）时只显示“运行中 · 未确认 / 未运行 · 未确认”，不出现“运行中”。
- `absent_at=0` 表示确认消失的时刻未知（升级前就已标记 absent 的记录）。页面按“未记录”展示，并另外给出最后观测时间；这些记录的保留期退回按最后观测时间计算，不会无限保留。
- 每节点最多保留 128 个最近实例，absent 历史最多保留 30 天（按 `absent_at`，未知时按最后观测时间）；删除节点级联清理。

### 清理与在途上报

面板删除单条记录或重置整个节点的观测数据时，会同时把自己签发的观测会话换成新的：观测会话只由服务端产生，探针拿不到旧会话的续期，于是**清理之前采集的分页无论何时到达都会被拒绝**——包括在清理之前就已经采集、但第一页在清理之后才到达的那些在途扫描。这个屏障不比较任何一方的时钟，也不靠永久忽略实例，因此清理后重新发现的实例照常入库。

已经收到过第一页的那一轮在清理时被整轮丢弃，后续分页不会再拼出半截快照。节点离线时没有会话可换（没有在途分页可挡，探针重连本来就会拿到新会话），此时所有分页都会被拒绝：当前会话为空说明这条连接没有有效观测会话，而 Hub 先摘连接表、再异步关连接并取消上下文，读循环里带着旧会话的回调随时可能落到观测服务，不能因为“连接已经不在表里”就放行。清理只作用于观测数据：不停止、重启或卸载任何代理，不删除二进制、配置、systemd 服务或托管记录，不清零真实流量计数，也不影响节点归属与业务数据。

- `usage.source=x_ui_database, scope=manager_total`：原管理器数据库保存的上下行累计、额度、到期时间；`collected_at` 是采集时间，并非原管理器最后结算时间。重置周期沿用原管理器，未确认时不标注“本月”。
- `source=sing-box_api/xray_api, scope=reference`：已有核心 API 的 `reset=false` 参考计数。不得据此保证增量、速率或账期准确性。Xray 使用 `/xray.app.stats.command.StatsService/QueryStats`；sing-box 使用 `/v2ray.core.app.stats.command.StatsService/QueryStats`，不混用服务名。
- 不可用用 `null` 或 `stats_status=not_configured/unavailable`；0 是有效实测值。
- 数据库采集只按普通文件读取稳定 DB/WAL，再在探针自有临时目录查询副本。原目录不创建 SHM、journal 或锁，不修改 schema，不 checkpoint 原库；总文件上限 32 MiB，查询限时 2 秒。
- 外部用量不进入 `subscriber_traffic*`、节点账单/流量校准、订阅头、配额或停用逻辑，不将入站与用户计数相加。
- 用量的刷新周期由探针决定；面板清理观测记录后，页面上的历史用量一并消失，直到探针重新采集。

## 管理员 REST

| 路由 | 结果 |
|---|---|
| `GET /api/servers/{id}/proxy-observations` | `management,online,supported,scan,instances`，包含当前实例与历史记录；`scan` 为 `null` 表示还没提交过完整快照，`?summary=1` 不返回入站数组 |
| `GET /api/servers/{id}/proxy-instances` | 同上，规范实例列表入口 |
| `GET /api/servers/{id}/proxy-instances/{instance}` | 单实例元数据，不含入站数组 |
| `GET /api/servers/{id}/proxy-instances/{instance}/inbounds?offset=0&limit=100` | 分页入站、总数和 stale；limit 为 1–100 |
| `POST /api/servers/{id}/proxy-observations/refresh` | 空请求或 `{}`；成功 202，离线/旧能力 409 |
| `DELETE /api/servers/{id}/proxy-observations/{instance}` | 删除该节点保存的这一条观测记录；实例标识非法 400，记录不存在 404；节点离线同样可用 |
| `POST /api/servers/{id}/proxy-observations/reset` | 清空该节点的全部观测快照；返回 `cleared,requested,online`。`requested=true` 只表示已向在线探针下发重新采集请求，不代表采集已经完成 |

删除与重置都只操作观测数据，并按 `server_id` 限定作用范围；写入 `proxy_observation.delete/reset` 审计记录。前端在调用后重新拉取观测缓存，被删除的内容不会继续显示。

所有接口使用管理员 JWT、`Cache-Control: no-store`。外部、旧探针及归属未知节点拒绝核心写入；服务端发送队列还有最后一道核心命令检查，Agent 在入队、执行、恢复、维护等路径独立核验资源所有权。托管资源包括二进制/unit/config/logrotate 的哈希和目录身份；非本项目资源、drop-in 或运行命令变化会禁用管理。

这是只读观测协议，不提供远程读取原文件、执行任意命令或编辑外部绑定的接口。手动绑定只能由节点本地 YAML 设置。
