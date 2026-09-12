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

- [ ] `vps-agent --once` 在一台 Linux 机器上输出一段 JSON，含 hello 与 metrics 两段；数值合理（CPU 0–100、内存已用小于总量、网卡不含 docker/veth）
- [ ] 连续运行时 `rx_rate/tx_rate` 与 `vnstat -l` 或 `iftop` 同量级
- [ ] 单元测试：网卡通配过滤、计数器回绕、`/proc/net/sockstat` 解析（用固定样本文本）
- [ ] 无服务端时 agent 持续重连，日志间隔符合退避且不刷屏（60s 上限）
- [ ] `install.sh` 在 Debian 12 与 Ubuntu 22.04 各装一次成功、重复执行成功、`uninstall.sh` 清理干净
- [ ] 两架构二进制能在对应机器执行 `--version`
- [ ] 常驻内存（RSS）小于 30 MB

## 7. 风险与注意

- gopsutil 的 `host.Info()` 在某些容器化 VPS（LXC/OpenVZ）上部分字段为空，降级填 `unknown`，不报错。
- 秒级 `cpu.Times` 需保留上次快照，不要用 `cpu.Percent(interval)` 阻塞采样。
- 磁盘 `Usage` 调用较慢，10 秒缓存足够（卡片显示也不需要秒级）。
- 脚本用 `#!/usr/bin/env bash`，用 `set -euo pipefail`，LF 换行。

## 8. 产出物

`proto/msg.go`、`agent/cmd/agent/main.go`、`agent/internal/{config,collector,transport}`、`agent/install.sh`、`agent/uninstall.sh`、`Makefile` 的 `build-agent`、`docs/protocol.md`（hello/metrics/config 定稿）。

## 9. 偏离记录

- 设计方案 §5.2 提到 agent 上报公网 IP；本步不做外网查询，改为服务端在 Hub 里用连接的来源地址填 `public_ip`（步骤 05）。
