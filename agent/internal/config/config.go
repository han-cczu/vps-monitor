// Package config 读取 agent 的 YAML 配置。
//
// 配置文件默认在 /etc/vps-agent/config.yaml，由 install.sh 写入（0600，里面有 token）。
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath 是配置文件的默认位置。
const DefaultPath = "/etc/vps-agent/config.yaml"

// 上报间隔的取值范围（秒）。服务端下发的 config 也按这个范围夹取。
const (
	MinReportInterval = 1
	MaxReportInterval = 60
)

// DefaultExcludeInterfaces 是默认不计入流量统计的网卡通配。
//
// 回环、容器网桥、veth 对、隧道都不是真实出口流量，算进去会让速率虚高一倍以上。
var DefaultExcludeInterfaces = []string{"lo", "docker*", "veth*", "br-*", "tun*", "tap*", "tailscale*", "wg*"}

// DefaultDiskMounts 是默认统计的挂载点。
var DefaultDiskMounts = []string{"/"}

// Config 是 agent 的运行配置。
type Config struct {
	Server         string     `yaml:"server"`          // wss://panel.example.com/api/agent/ws
	Token          string     `yaml:"token"`           // agent token，面板创建节点时给的
	ReportInterval int        `yaml:"report_interval"` // 秒，服务端 config 消息可覆盖
	Interfaces     Interfaces `yaml:"interfaces"`
	DiskMounts     []string   `yaml:"disk_mounts"` // 磁盘统计的挂载点，多项求和
	LogLevel       string     `yaml:"log_level"`   // debug / info / warn / error
}

// Interfaces 是网卡过滤规则。
type Interfaces struct {
	Exclude []string `yaml:"exclude"`
}

// Load 读取并解析配置文件，填好默认值。
//
// 只做格式与取值范围检查；`server` / `token` 是否齐全由 Validate 判断
// （--once 模式不联网，缺这两项也能跑）。
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse 解析配置内容，填好默认值。单测直接用它，不碰文件系统。
func Parse(raw []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}

	cfg.Server = strings.TrimSpace(cfg.Server)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.LogLevel = strings.TrimSpace(cfg.LogLevel)

	if cfg.ReportInterval == 0 {
		cfg.ReportInterval = MinReportInterval
	}
	if cfg.ReportInterval < MinReportInterval || cfg.ReportInterval > MaxReportInterval {
		return Config{}, fmt.Errorf("report_interval 需要在 %d–%d 秒之间，当前为 %d",
			MinReportInterval, MaxReportInterval, cfg.ReportInterval)
	}
	if cfg.Interfaces.Exclude == nil {
		cfg.Interfaces.Exclude = append([]string(nil), DefaultExcludeInterfaces...)
	}
	if len(cfg.DiskMounts) == 0 {
		cfg.DiskMounts = append([]string(nil), DefaultDiskMounts...)
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if _, err := ParseLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate 检查联网所需的字段。--once 模式不调用它。
func (c Config) Validate() error {
	if c.Server == "" {
		return fmt.Errorf("配置里缺 server（形如 wss://panel.example.com/api/agent/ws）")
	}
	u, err := url.Parse(c.Server)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fmt.Errorf("server 必须是 ws:// 或 wss:// 开头的完整地址，当前为 %q", c.Server)
	}
	if c.Token == "" {
		return fmt.Errorf("配置里缺 token（面板创建节点时给的那串）")
	}
	return nil
}

// ParseLogLevel 把 debug / info / warn / error 解析成 slog.Level。
func ParseLogLevel(s string) (slog.Level, error) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo, fmt.Errorf("log_level %q 无效（可选 debug / info / warn / error）", s)
	}
	return lv, nil
}
