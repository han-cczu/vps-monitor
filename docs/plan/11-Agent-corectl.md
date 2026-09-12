# 步骤 11 · Agent corectl

> 阶段 3 · 依赖：04、10 · 粗估 4–5 天 · 对应设计方案 §5.4、§7.1、§7.2、§13

## 1. 目标

agent 具备受控管理 sing-box 的全部能力：安装指定版本、应用一份完整配置（校验、备份、替换、重启、端口检查、失败回滚）、启停重启、回传日志、每 10 秒上报每用户流量增量、放行端口、周期上报核心状态。只认白名单消息。

## 2. 范围

**做**：`corectl` 包全部、消息 schema 校验、`proto` 新增 `core.*` 消息、单元测试、本地调试入口。

**不做**：服务端渲染与下发（13）。本步用 `vps-agent core apply --file` 本地调试子命令（见 4.5）与单测验收。

## 3. 前置条件

步骤 10 完成（面板可下载 sing-box；统计已验证）。

## 4. 实现方案

### 4.1 消息 `proto/core.go`

```go
const ( TypeCoreApply="core.apply"; TypeCoreAction="core.action"; TypeCoreLogsReq="core.logs"
        TypeCoreState="core.state"; TypeCoreStats="core.stats"; TypeCoreLogs="core.logs" )
type CoreApply  struct { Type string; Revision int64; Core, Version, ConfigSHA256 string; Ports []string; Config json.RawMessage }
type CoreAction struct { Type, Action, Version, File, SHA256 string; ReqID string }   // install|start|stop|restart
type CoreLogsReq struct { Type, Kind string; Lines int; ReqID string }
type CoreState  struct { Type, Core, InstalledVersion string; Running bool; AppliedRevision int64
                         ConfigSHA256 string; Listening []string; Firewall string; Error *string; ReqID string }
type CoreStats  struct { Type string; TS int64; Inbounds []Counter; Users []Counter }   // Counter{Name string; Up, Down int64}
type CoreLogs   struct { Type, Kind, Text, ReqID string }
```

`ReqID` 用于日志与动作的请求-响应关联。

### 4.2 包结构 `agent/internal/corectl`

| 文件 | 职责 |
|---|---|
| `manager.go` | 入口：`Handle(msg)` 按类型分发；**单飞**：同一时刻只跑一个 install/apply/action，其余排队（队列长度 4，满则丢弃并回报错误）；每 60 秒与状态变化时发 `core.state` |
| `paths.go` | 常量：二进制、配置、备份、日志、状态文件 `/var/lib/vps-agent/core.json`（记录 applied_revision、sha256、installed_version） |
| `install.go` | 下载（面板 `GET /api/agent/corefiles/{version}/{arch}`，Bearer token，60 s 超时，写 `.tmp`）→ sha256 校验 → `chmod 755` → `sing-box.tmp version` 能跑 → `rename` 覆盖 → 写 unit（若不存在或内容不同）→ `daemon-reload`、`enable` → 记录 installed_version |
| `apply.go` | 设计 §5.4 的 7 步；`Validate(CoreApply)`：revision 大于 0、version 非空、`Config` 是 JSON 对象且小于 1 MB、`Ports` 每项匹配 `^\d{1,5}/(tcp|udp)$`；配置写入前先 `json.Compact` |
| `systemd.go` | `exec.CommandContext` 调 `systemctl` 子命令（`is-active`、`restart`、`start`、`stop`、`daemon-reload`、`enable`），全部 30 s 超时；`ServiceUnit()` 返回 unit 文本 |
| `ports.go` | 解析 `/proc/net/{tcp,tcp6,udp,udp6}`：`st==0A` 的 tcp 为 LISTEN；udp 取本地端口存在即视为监听；返回 `[]string{"443/tcp",...}`；`Missing(expected, actual)` |
| `firewall.go` | 检测：`ufw status` 含 `Status: active` → `ufw allow {port}/{proto}`；存在 `firewall-cmd` 且 `--state` 为 running → `--permanent --add-port={port}/{proto}` + `--reload`；否则 `none`。只放行不撤销（撤销留二期，并在 `core.state.firewall` 报类型） |
| `stats.go` | gRPC 客户端（生成代码放 `proto/singbox/v2rayapi`，由步骤 10 拷来的 `stats.proto` 生成，`protoc-gen-go` + `grpc`）；每 10 s `QueryStats{Pattern:"", Reset_:true}`；解析名字 `user>>>NAME>>>traffic>>>uplink|downlink`、`inbound>>>TAG>>>...`；聚合成 `CoreStats` 发出；连接失败每 60 s 重试，错误写入 `core.state.error` |
| `logs.go` | 读 `/var/log/sing-box/box.log` 尾部 N 行（N ≤ 1000，从文件尾反向按块读）；`kind` 目前只有 `error`（sing-box 单日志文件），`access` 预留 |
| `state.go` | 组装 `CoreState`：`installed_version` 用 `sing-box version` 解析（缓存，安装后刷新）、`running` 用 `is-active`、`listening` 用 ports、`applied_revision/sha` 读状态文件 |

### 4.3 应用流程细化（`apply.go`）

```
Validate → 若 revision ≤ applied && sha 相同 && running → 直接回 state
写 config.json.new → check（sing-box check -c）失败 → 回 state{error: stderr 前 2 KB}，删 .new
备份 config.json → config.json.bak（不存在则跳过）
rename .new → config.json
firewall 放行 Ports
systemctl restart sing-box
轮询 ≤ 5 s：is-active 且 Missing(Ports, listening) 为空 → 成功：写状态文件、回 state
失败 → 若有 .bak：rename 回 → restart → 回 state{error:"applied failed, rolled back: ..."}
        若无 .bak：stop → 回 state{running:false, error}
```

回滚后 `applied_revision` 保持旧值，服务端据此知道下发未生效。

### 4.4 与传输层的接线

- `transport` 收到 `core.*` 交给 `corectl.Manager.Handle`；`Manager` 通过 `send` 回调发 `core.state/stats/logs`。
- `hello` 增加 `applied_revision`（从状态文件读）；连接建立后立刻发一次 `core.state`。
- 进程状态轮询：每 10 s `is-active`，从 running 变为非 running 时立即发 `core.state`（sing-box 崩溃能被面板及时看到）。

### 4.5 本地调试入口

`vps-agent core apply --file cfg.json --version v1.x --ports 443/tcp,8443/udp` 与 `vps-agent core state`、`vps-agent core stats`：不连服务端，直接调用 `corectl`，输出 JSON。用于本步验收与日后排障。

### 4.6 卸载脚本

`uninstall.sh --purge-core`：stop/disable `sing-box`、删二进制、`/etc/sing-box`、日志。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| WS | server → agent：`core.apply`、`core.action`、`core.logs`；agent → server：`core.state`、`core.stats`、`core.logs` |
| 文件 | `/usr/local/bin/sing-box`、`/etc/sing-box/config.json(.bak/.new)`、`/var/lib/vps-agent/core.json`、`sing-box.service` |

## 6. 验收标准

- [ ] 单测：`Validate` 各分支；`ports.go` 用固定 `/proc/net/tcp` 样本；`stats.go` 计数器名解析；`apply` 用 fake `systemctl`/`check` 接口覆盖"check 失败"、"重启后端口缺失回滚"、"无 bak 时 stop"三条路径
- [ ] 测试节点：`core apply --file` 一份合法配置 → sing-box 运行、端口监听、`core state` 显示 applied；改成非法配置再 apply → check 失败、原配置未动、服务仍运行
- [ ] 把配置里的端口改成被占用端口 → 重启后端口检查失败 → 自动回滚到 `.bak`，服务恢复
- [ ] `core stats` 在有流量时输出非零增量，连续两次第二次接近零（reset 生效）
- [ ] ufw 开启的机器上 apply 后 `ufw status` 出现对应规则
- [ ] `kill -9` sing-box 进程 → 10 s 内 agent 日志出现状态变更（发送 `core.state running=false`）
- [ ] 所有外部命令都有超时，不存在的 `systemctl` 报清晰错误而不是挂死
- [ ] agent 不响应任何未定义消息类型（发一条 `{"type":"exec"}` 只留 warn 日志）

## 7. 风险与注意

- `sing-box check` 只检查语法与字段，不检查端口占用，所以重启后的端口检查不可省。
- `rename` 覆盖二进制时若 sing-box 正在运行，Linux 允许（旧 inode 继续用），下一次 restart 生效。
- 生成 gRPC 代码要引入 `google.golang.org/grpc`，agent 体积增加约 5 MB，可接受。
- 不要在 agent 里实现"删除端口放行"，误删会把机器锁在外面。

## 8. 产出物

`proto/core.go`、`proto/singbox/v2rayapi/*.pb.go`、`agent/internal/corectl/*`、`agent/cmd/agent` 子命令、`uninstall.sh --purge-core`、`protocol.md`（core.* 定稿）。

## 9. 偏离记录

（开工前为空）
