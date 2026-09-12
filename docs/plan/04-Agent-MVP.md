# 步骤 04 · Agent MVP

> 阶段 1 · 依赖：01 · 粗估 3 天 · 对应设计方案 §5.1–§5.3、§5.5、§7.1

## 1. 目标

一个 Linux 单文件 agent：采集静态信息与秒级指标，探测 IPv4/IPv6，通过 WSS 上报，断线重连；配一键安装/卸载脚本。sing-box 相关（corectl）不在本步。

## 2. 范围

**做**：配置、采集、传输、重连、`--once` 调试输出、install.sh / uninstall.sh、systemd unit、交叉编译。

**不做**：ping 任务（09）、corectl（11）、自动更新（20）。服务端的 WS 接入在步骤 05，本步用 `--once` 与单元测试验收。

## 3. 前置条件

步骤 01 完成（`proto` 模块存在）。

## 4. 实现方案

### 4.1 共享消息 `proto/msg.go`

```go
const (
    TypeHello = "hello"; TypeMetrics = "metrics"; TypePing = "ping"
    TypeConfig = "config"
)
type Envelope struct{ Type string `json:"type"` }        // 先解 type 再分发
type HostInfo struct {
    Hostname, OS, Kernel, Arch, CPUModel string
    Cores int; MemTotal, DiskTotal, BootTime int64
    IPv4, IPv6 bool
}
type Hello struct { Type, Version string; AppliedRevision int64; Host HostInfo }
type Metrics struct {
    Type string; TS int64; CPU float64; MemUsed, SwapUsed, DiskUsed int64
    Load [3]float64; Net NetStat; TCP, UDP, Procs int; Uptime int64
}
type NetStat struct { RxTotal, TxTotal, RxRate, TxRate int64 }
type Config struct { Type string; ReportInterval int; PingTasks []PingTask }
type PingTask struct { ID int64; Name, Target, Kind string; Interval int }
```

字段 json tag 用蛇形（`mem_used`），与设计方案 §7 一致。

### 4.2 配置 `agent/internal/config`

`/etc/vps-agent/config.yaml`：

```yaml
server: wss://panel.example.com/api/agent/ws
token: "..."
report_interval: 1          # 秒；服务端 config 消息可覆盖
interfaces:
  exclude: ["lo", "docker*", "veth*", "br-*", "tun*", "tailscale*"]   # 默认值，可覆盖
disk_mounts: ["/"]          # 磁盘统计的挂载点，多项求和
log_level: info
```

命令行：`--config`（默认上面路径）、`--once`（采集一次打印 JSON 退出，不联网）、`--version`。

### 4.3 采集 `agent/internal/collector`

| 文件 | 内容 |
|---|---|
| `host.go` | `CollectHost() HostInfo`：`host.Info()`（hostname、os+platform version、kernel、arch、boot time）、`cpu.Info()` 型号、`runtime.NumCPU()`、`mem.VirtualMemory().Total`、各挂载点 `disk.Usage().Total` 求和 |
| `ipcheck.go` | `ProbeIPv4/6()`：`net.Dialer{Timeout: 3s}` 分别 `tcp4` 拨 `1.1.1.1:443`、`tcp6` 拨 `[2606:4700:4700::1111]:443`，成功即 true；两个地址可配置 |
| `metrics.go` | `Sampler` 持有上次 CPU 时间片与网卡计数；`Sample() Metrics`：CPU 用 `cpu.Times(false)` 两次差值算百分比；`mem.VirtualMemory().Used`、`SwapMemory().Used`；磁盘每 10 秒刷新一次缓存；`load.Avg()`；`Uptime` 用 `host.Uptime()` |
| `net.go` | `net.IOCounters(true)` 按 exclude 通配过滤后求和；速率 = 差值 / 实际间隔秒；处理计数器回绕（新值小于旧值时速率记 0） |
| `conn.go` | 解析 `/proc/net/sockstat` 与 `sockstat6` 的 `TCP: inuse`、`UDP: inuse`；进程数读 `/proc` 下纯数字目录计数 |

采集失败的单项记 0 并 `slog.Warn`，不让整条 metrics 缺席。

### 4.4 传输 `agent/internal/transport`

```go
type Client struct { url, token, version string; onMessage func(env json.RawMessage) }
func (c *Client) Run(ctx)            // 循环：连接→hello→收发→断开→退避重连
func (c *Client) Send(v any) error   // 带 5s 写超时；未连接时丢弃并返回 ErrNotConnected
```

- `coder/websocket.Dial` 带 header `Authorization: Bearer {token}`、`X-Agent-Version`；`CompressionMode: ContextTakeover`。
- 连接成功立刻发 `hello`；启动读协程分发 `config`（更新上报间隔；ping 任务交给 09 的处理器，本步只记日志）。
- 心跳：每 20 秒 `Ping(ctx)`，超时 10 秒视为断线。
- 重连退避 1s、2s、4s … 上限 60s，成功后归零。
- 采集协程独立于连接：`ticker` 按 `report_interval` 采样，连上才发，没连上丢弃（累计流量靠计数器，不受影响）。
- 静态信息每 5 分钟重发一次 `hello`（服务端幂等更新）。

### 4.5 主程序与构建

- `cmd/agent/main.go`：解析参数 → 加载配置 → `--once` 分支 → 否则 `transport.Run`；处理 SIGTERM 优雅退出。
- 构建：`CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/agent/vps-agent-linux-{arch}`。
- 版本号来自 git tag（`git describe --tags --always`）。

### 4.6 安装脚本 `agent/install.sh`

```
用法：install.sh --server wss://host/api/agent/ws --token TOKEN [--base https://host]
```

1. `root` 检查；`systemctl` 存在检查
2. 架构：`uname -m` → `x86_64→amd64`、`aarch64→arm64`，否则退出
3. `--base` 缺省由 `--server` 推导（`wss://`→`https://`，去掉路径）
4. `curl -fsSL {base}/agent/vps-agent-linux-{arch} -o /usr/local/bin/vps-agent.tmp` → `chmod 755` → `mv`
5. 写 `/etc/vps-agent/config.yaml`（0600），已存在则只更新 `server`/`token`
6. 写 `/etc/systemd/system/vps-agent.service`：

```
[Unit]
Description=VPS Monitor Agent
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/usr/local/bin/vps-agent --config /etc/vps-agent/config.yaml
Restart=always
RestartSec=3
LimitNOFILE=65536
[Install]
WantedBy=multi-user.target
```

7. `systemctl daemon-reload && systemctl enable --now vps-agent`，打印 `systemctl status` 摘要与 `journalctl -u vps-agent -n 5`
8. 幂等：重复执行等于升级 + 重启

`uninstall.sh`：stop/disable、删 unit、二进制、`/etc/vps-agent`、`/var/lib/vps-agent`；`--purge-core` 参数留给步骤 11。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| WS 消息 | `hello`、`metrics`（agent → server）；`config`（server → agent，本步只处理 `report_interval`） |
| 文件 | `/etc/vps-agent/config.yaml`、`/usr/local/bin/vps-agent`、`vps-agent.service` |

## 6. 验收标准

- [ ] `vps-agent --once` 在一台 Linux 机器上输出一段 JSON，含 hello 与 metrics 两段；数值合理（CPU 0–100、内存已用小于总量、网卡不含 docker/veth）——**待真机**（本机 Windows 上已跑通，数值合理）
- [ ] 连续运行时 `rx_rate/tx_rate` 与 `vnstat -l` 或 `iftop` 同量级——**待真机**
- [x] 单元测试：网卡通配过滤、计数器回绕、`/proc/net/sockstat` 解析（用固定样本文本）
- [x] 无服务端时 agent 持续重连，日志间隔符合退避且不刷屏（60s 上限）
- [ ] `install.sh` 在 Debian 12 与 Ubuntu 22.04 各装一次成功、重复执行成功、`uninstall.sh` 清理干净——**待真机**
- [ ] 两架构二进制能在对应机器执行 `--version`——**待真机**（两个架构都编译通过）
- [ ] 常驻内存（RSS）小于 30 MB——**待真机**

## 7. 风险与注意

- gopsutil 的 `host.Info()` 在某些容器化 VPS（LXC/OpenVZ）上部分字段为空，降级填 `unknown`，不报错。
- 秒级 `cpu.Times` 需保留上次快照，不要用 `cpu.Percent(interval)` 阻塞采样。
- 磁盘 `Usage` 调用较慢，10 秒缓存足够（卡片显示也不需要秒级）。
- 脚本用 `#!/usr/bin/env bash`，用 `set -euo pipefail`，LF 换行。

## 8. 产出物

`proto/msg.go`、`agent/cmd/agent/main.go`、`agent/internal/{config,collector,transport}`、`agent/install.sh`、`agent/uninstall.sh`、`Makefile` 的 `build-agent`、`docs/protocol.md`（hello/metrics/config 定稿）。

## 9. 偏离记录

> 实施日期：2026-09-12

- 设计方案 §5.2 提到 agent 上报公网 IP；本步不做外网查询，改为服务端在 Hub 里用连接的来源地址填 `public_ip`（步骤 05）。（开工前就写在这里的一条）

### 协议

1. **消息改回扁平结构。** 步骤 01 在 `protocol.md` 里定过一版 `{type, id, ts, data}` 的信封，
   但设计方案 §7 的报文从头到尾都是扁平的（`type` 是消息自己的字段）。本步定稿时改回扁平，
   `proto.Envelope` 退化成只有 `Type` 的探测结构：先解它拿类型，再把同一段 JSON 解成具体结构体。
   需要请求/响应配对的消息（步骤 11 的 `core.logs`）自己带字段配对。`protocol.md` §1 已整节改写。
2. ping 结果的 type 用 **`ping`**（设计方案 §7.1 的写法），不是步骤 01 骨架里的 `ping.result`。
3. `hello` 保留步骤 01 加的 **`proto_version`**（设计方案的示例里没有）：步骤 05 要靠它判断协议兼容性。

### 依赖

4. **agent 与服务端统一用 `coder/websocket`。** 步骤 05 文档写的是 `gorilla/websocket`，步骤 02 也为此预留了一个 indirect 依赖。
   两边不同库能跑但没必要；选 coder 是因为它的 API 原生吃 `context`（读写超时、优雅关闭都靠 ctx），
   而 gorilla 归档过一次、重启维护后仍是老式的 SetReadDeadline 风格。步骤 05 实现服务端时把 gorilla 这个 indirect 清掉。
5. agent 模块跑了 `go mod tidy`（步骤 01 备注说等用上再 tidy）。
   连带把尚未使用的 `pro-bing`（步骤 09 的 ICMP）、`uuid`、`x/net`、`x/sync` 删掉了，用到时再 `go get`。

### 采集

6. **CPU 百分比排除 `guest` / `guest_nice`。** Linux 把这两项同时记在 `user` / `nice` 里，
   照 gopsutil 的字段全加一遍会重复计入，虚拟化机器上能虚高一截。公式是 `(总增量 − idle增量 − iowait增量) / 总增量`，结果夹在 0–100。
7. **连接数读 `/proc/net/sockstat` 与 `sockstat6` 的 `inuse`**，没用 gopsutil 的 `net.Connections`：
   后者要遍历 `/proc/*/fd` 把每个套接字解析一遍，连接数上万的机器上一次几百毫秒，秒级采集扛不住。
   解析写成纯函数（`parseSockstat`），用固定样本做表驱动单测。
8. **磁盘按设备去重。** `disk_mounts` 里两个路径指向同一个设备（bind mount）时只算一次，否则容量凭空翻倍。
9. **网卡通配只支持末尾一个 `*`**，自己实现而不是用 `filepath.Match`：网卡名里出现 `[` 之类字符会让 Match 直接返回错误。
   默认排除表比文档多了 `tap*` 与 `wg*`（TAP 设备与 WireGuard 都不是真实出口流量）。
10. **IPv4 / IPv6 探测并行**（最坏只花一个超时），结果在 main 里缓存 5 分钟：
    没有 v6 的机器每次重连都要等满 3 秒超时，不该让 hello 为此卡住。

### 传输

11. **退避归零的条件是「连接活过 30 秒」**，不是「连上就归零」。
    否则遇到「能连上但立刻被关」（token 刚被重置、服务端正在滚动重启）会退化成每秒重连一次。
12. 401 单独识别并在错误信息里直说是 token 的问题——否则用户看到的是一长串 WebSocket 握手错误，无从下手。
13. `hello` 用回调提供而不是固定值：重连时机器可能已经加过内存、换过内核，重发的静态信息应该是新的。

### 主程序

14. **`--once` 在没有配置文件时按默认值跑**（文档没写这个情况），方便在还没装过的机器上直接 `./vps-agent --once` 对数；
    并且内部**采两次样、中间隔满 1 秒**——CPU 与速率都是差值算出来的，只采一次打印出来永远是 0。
15. 上报间隔用 `atomic.Int64` + `timer.Reset` 传递，服务端下发 `config` 后**下一帧**就生效（实测 1s → 3s）。

### 脚本

16. **install.sh 下载后先跑一次 `--version` 再原子替换**：既避免把正在运行的二进制写坏（先写临时文件再 `mv`），
    也能挡住架构不匹配或下载到半截 HTML 的情况。
17. **已有配置只改 `server` 与 `token` 两行**（用 awk，缺哪行补哪行），用户调过的上报间隔、网卡过滤、挂载点都保留。
18. `Makefile` 的 `build-agent` 顺带把 `install.sh` / `uninstall.sh` 拷进 `dist/agent/`，
    整个目录直接放进 `{VM_DATA_DIR}/agent/` 就能用（步骤 03 的下载白名单正好是这四个文件名）。

### 留给后续步骤

19. 服务端的 `/api/agent/ws` 要到步骤 05 才有，本步用一个临时 mock hub 做端到端验证。
20. `config` 里的 `ping_tasks` 只记一条日志，步骤 09 才执行。
21. `core.*` 消息类型已在 `proto` 里占好位，步骤 11 / 13 填。

### 验收情况

**本机（Windows）已验**：

- `go vet` / `go test ./proto/... ./agent/... ./server/...` 全绿；两个架构交叉编译通过（amd64 7.2 MB、arm64 6.7 MB）
- 单测覆盖网卡通配过滤（含 `lo` 不误伤 `local`、`br-*` 不误伤 `br0`）、计数器回绕、sockstat / sockstat6 解析与畸形输入、
  CPU 百分比（含满载、全空闲、计数器回退）、配置默认值与校验、第一帧没有速率；
  transport 侧用 httptest 起真 WebSocket 服务端，验了握手头、hello、config 分发、未连接时 `Send` 返回 `ErrNotConnected`、
  服务端关闭后自动重连、401 的错误信息
- `--once` 输出合理：CPU 14.32%、内存 35/51 GB、磁盘、网卡速率都对得上任务管理器（`tcp`/`udp`/`procs` 在 Windows 上是 0，符合预期）
- 连不上时的退避实测 1s → 2s → 4s → 8s，日志一次一行不刷屏
- 用临时 mock hub（冒充步骤 05 的 `/api/agent/ws`）跑真实二进制端到端：
  鉴权头正确、第一条是 hello、metrics 每秒一条（实测间隔 1.01 秒）、下发 `config{report_interval:3}` 后间隔变成 3 秒

**待真机（Linux VPS）**：第 6 节里标了「待真机」的五条——`--once` 的数值、速率与 `vnstat` 对比、
install.sh 在 Debian / Ubuntu 上的安装与重复安装、`uninstall.sh` 清理、两架构 `--version`、RSS < 30 MB。
