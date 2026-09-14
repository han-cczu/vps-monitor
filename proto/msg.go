// Package proto 定义 agent 与 server 之间 WebSocket 消息的结构体与常量。
//
// 这个包必须保持零依赖（只用标准库），agent 与 server 都直接引用它。
//
// 消息是**扁平**结构：`type` 是消息自身的一个字段，不套 data 信封（见设计方案 §7）。
// 收到一帧先解成 Envelope 拿 Type，再按类型解成对应结构体。
// 时间戳：`ts` 是 Unix 秒，字段名用蛇形。
package proto

// Version 是协议版本。agent 在 hello 里带上，server 用它做兼容判断。
const Version = 1

// 消息类型常量。
const (
	// agent -> server
	TypeHello     = "hello"      // 连上后第一条：静态信息 + 已应用修订号
	TypeMetrics   = "metrics"    // 每秒一条：动态采集
	TypePing      = "ping"       // ping 任务结果（步骤 09）
	TypeCoreState = "core.state" // sing-box 实际状态（步骤 11）
	TypeCoreStats = "core.stats" // 每入站 / 每用户流量增量（步骤 13）
	TypeCoreLogs  = "core.logs"  // 日志尾部回传（步骤 11）
	TypeError     = "error"      // agent 侧执行失败

	// server -> agent
	TypeConfig     = "config"      // 上报间隔、ping 任务等运行参数
	TypeCoreAction = "core.action" // install / start / stop / restart（步骤 11）
	TypeCoreApply  = "core.apply"  // 下发一份完整 sing-box 配置（步骤 13）
)

// Envelope 只用来探测消息类型：先解它拿 Type，再把同一段 JSON 解成具体结构。
type Envelope struct {
	Type string `json:"type"`
}

// HostInfo 是节点的静态信息，agent 连上后（以及每 5 分钟）重发一次。
type HostInfo struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`     // 发行版 + 版本，如 "Debian 12"
	Kernel    string `json:"kernel"` // 内核版本
	Arch      string `json:"arch"`   // x86_64 / aarch64
	CPUModel  string `json:"cpu_model"`
	Cores     int    `json:"cores"`      // 逻辑核数
	MemTotal  int64  `json:"mem_total"`  // 字节
	DiskTotal int64  `json:"disk_total"` // 字节，配置里的挂载点求和
	BootTime  int64  `json:"boot_time"`  // Unix 秒
	IPv4      bool   `json:"ipv4"`       // 探测到的外网 IPv4 可达性
	IPv6      bool   `json:"ipv6"`
}

// Hello 是 agent 连上后的第一条消息。
type Hello struct {
	Capabilities    []string `json:"capabilities,omitempty"`
	Type            string   `json:"type"`
	ProtoVersion    int      `json:"proto_version"`
	Version         string   `json:"version"`          // agent 版本
	AppliedRevision int64    `json:"applied_revision"` // 已应用的 sing-box 配置修订号，步骤 11 起才非 0
	Host            HostInfo `json:"host"`
}

// NetStat 是网卡的累计值与瞬时速率（字节 / 字节每秒）。
type NetStat struct {
	RxTotal int64 `json:"rx_total"`
	TxTotal int64 `json:"tx_total"`
	RxRate  int64 `json:"rx_rate"`
	TxRate  int64 `json:"tx_rate"`
}

// Metrics 是每秒一条的动态采集。
type Metrics struct {
	Type     string     `json:"type"`
	TS       int64      `json:"ts"`        // 采集时刻，Unix 秒
	CPU      float64    `json:"cpu"`       // 0–100
	MemUsed  int64      `json:"mem_used"`  // 字节
	SwapUsed int64      `json:"swap_used"` // 字节
	DiskUsed int64      `json:"disk_used"` // 字节
	Load     [3]float64 `json:"load"`      // 1 / 5 / 15 分钟
	Net      NetStat    `json:"net"`
	TCP      int        `json:"tcp"`
	UDP      int        `json:"udp"`
	Procs    int        `json:"procs"`
	Uptime   int64      `json:"uptime"` // 秒
}

// PingTask 是服务端下发的一条 ping 任务，agent 收到 config 后整体对齐。
type PingTask struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Target   string `json:"target"`
	Kind     string `json:"kind"`     // icmp | tcp
	Interval int    `json:"interval"` // 秒
}

// PingResult 是一次 ping 任务的结果，LatencyMS 为 nil 表示丢包（步骤 09）。
type PingResult struct {
	Type      string   `json:"type"`
	TaskID    int64    `json:"task_id"`
	TS        int64    `json:"ts"`
	LatencyMS *float64 `json:"latency_ms"`
}

// Config 是 server 下发给 agent 的运行参数。ReportInterval 为 0 表示不改。
type Config struct {
	Type           string     `json:"type"`
	ReportInterval int        `json:"report_interval"` // 秒
	PingTasks      []PingTask `json:"ping_tasks"`
}

// Error 是 agent 执行失败时回报的消息。
type Error struct {
	Type    string `json:"type"`
	Op      string `json:"op"` // 出错的操作，如 "core.apply"
	Message string `json:"message"`
}
