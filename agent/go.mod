module vpsmon/agent

go 1.26.0

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/prometheus-community/pro-bing v0.9.1 // indirect
	github.com/shirou/gopsutil/v4 v4.26.8 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	vpsmon/proto v0.0.0
)

replace vpsmon/proto => ../proto
