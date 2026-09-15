package proto

// External observation has no configuration, credential or write-command fields.
const ProxyObserveCapability = "proxy.observe.v1"
const TypeProxyObservation = "proxy.observation"
const TypeProxyRefresh = "proxy.refresh"
const ProxyMaxFrame = 256 << 10
const ProxyMaxPages = 128
const ProxyMaxInstances = 32
const ProxyMaxInbounds = 1024

type ObservedPort struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}

type ObservedUsage struct {
	Source      string `json:"source"`
	Scope       string `json:"scope"`
	Up          *int64 `json:"up"`
	Down        *int64 `json:"down"`
	Limit       *int64 `json:"limit"`
	ExpireAt    *int64 `json:"expire_at"`
	CollectedAt int64  `json:"collected_at"`
}

type ObservedInbound struct {
	ID        string         `json:"id"`
	Tag       string         `json:"tag"`
	Protocol  string         `json:"protocol"`
	Listen    string         `json:"listen"`
	Port      string         `json:"port"`
	Transport string         `json:"transport"`
	TLS       bool           `json:"tls"`
	Reality   bool           `json:"reality"`
	Users     *int           `json:"users"`
	Enabled   *bool          `json:"enabled"`
	InConfig  bool           `json:"in_config"`
	Listening *bool          `json:"listening"`
	Usage     *ObservedUsage `json:"usage"`
}

type ObservedInstance struct {
	ID             string            `json:"id"`
	Core           string            `json:"core"`
	Source         string            `json:"source"`
	Ownership      string            `json:"ownership"`
	Version        string            `json:"version"`
	Running        bool              `json:"running"`
	PID            int               `json:"pid"`
	ProcessStart   string            `json:"process_start"`
	ManagerPID     int               `json:"manager_pid"`
	ManagerRunning *bool             `json:"manager_running"`
	Service        string            `json:"service"`
	Binary         string            `json:"binary"`
	ConfigPaths    []string          `json:"config_paths"`
	Namespace      string            `json:"namespace"`
	RSSBytes       *int64            `json:"rss_bytes"`
	Ports          []ObservedPort    `json:"ports"`
	Inbounds       []ObservedInbound `json:"inbounds"`
	StatsStatus    string            `json:"stats_status"`
	Issues         []string          `json:"issues"`
	ConfigReadAt   int64             `json:"config_read_at"`
	LastSuccess    int64             `json:"last_success"`
	Stale          bool              `json:"stale"`
	Absent         bool              `json:"absent"`
	Truncated      bool              `json:"truncated"`
}

// Ordered pages form a single snapshot. The session is issued by the server for
// the current authenticated connection, preventing replay across reconnections.
type ProxyObservation struct {
	Type         string             `json:"type"`
	Session      string             `json:"session"`
	Sequence     int64              `json:"sequence"`
	Page         int                `json:"page"`
	Pages        int                `json:"pages"`
	ScanComplete bool               `json:"scan_complete"`
	CollectedAt  int64              `json:"collected_at"`
	Instances    []ObservedInstance `json:"instances"`
}
