// Package proto 定义 agent 与 server 之间 WebSocket 消息的结构体与常量。
//
// 这个包必须保持零依赖（只用标准库），agent 与 server 都直接引用它。
// 步骤 01 只放骨架，字段在后续步骤（04 / 05 / 09 / 11 / 13）逐步补全。
package proto

// 协议版本。握手时由 agent 带上，server 用它做兼容判断。
const Version = 1

// 消息类型常量。envelope 的 Type 字段只取这些值。
const (
	// agent -> server
	TypeHello      = "hello"       // 连上后第一条：静态信息 + 已应用修订号
	TypeMetrics    = "metrics"     // 每秒一条：动态采集
	TypePingResult = "ping.result" // ping 任务结果
	TypeCoreState  = "core.state"  // sing-box 实际状态
	TypeCoreStats  = "core.stats"  // 每入站 / 每用户流量增量
	TypeCoreLogs   = "core.logs"   // 日志尾部回传
	TypeError      = "error"       // agent 侧执行失败

	// server -> agent
	TypeConfig      = "config"        // 上报间隔、ping 任务等运行参数
	TypeCoreAction  = "core.action"   // install / start / stop / restart
	TypeCoreApply   = "core.apply"    // 下发一份完整 sing-box 配置
	TypeCoreLogsReq = "core.logs.req" // 索取日志
)

// Envelope 是所有消息的外层信封：先解 Type，再按类型解 Data。
type Envelope struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"` // 请求/响应配对用，单向消息可空
	TS   int64  `json:"ts"`           // 发送时刻，Unix 毫秒
	Data any    `json:"data,omitempty"`
}

// Hello 是 agent 连上后的第一条消息（步骤 04 填充）。
type Hello struct {
	ProtoVersion int    `json:"proto_version"`
	AgentVersion string `json:"agent_version"`
}

// Metrics 是每秒一条的动态采集（步骤 04 填充）。
type Metrics struct {
	TS int64 `json:"ts"` // 采集时刻，Unix 秒
}

// PingResult 是一次 ping 任务的结果（步骤 09 填充）。
type PingResult struct {
	TaskID int64 `json:"task_id"`
}

// Config 是 server 下发给 agent 的运行参数（步骤 05 / 09 填充）。
type Config struct {
	MetricsIntervalMS int `json:"metrics_interval_ms"`
}
