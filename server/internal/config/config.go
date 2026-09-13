// Package config 读取服务端配置。
//
// 只读环境变量（前缀 VM_），不读配置文件；缺省值见各字段注释。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DataDirPerm 是数据目录权限：只有属主能进。目录里有密码哈希和 JWT 密钥，
// 后续步骤还会放 agent token 与订阅凭据，不能让同机其他用户读到。
const DataDirPerm = 0o700

// JWTSecretFile 是自动生成的 JWT 密钥文件名（位于 DataDir 下）。
const JWTSecretFile = "jwt.secret"

// TrustedProxies 说明这台服务端前面有几层可信反向代理，决定客户端真实 IP 怎么取。
//
// 零值是"直连"：IP 只认 TCP 连接的对端地址，任何请求头都不采信。必须与实际部署配对，
// 否则登录限速和审计里的 IP 都能被请求头伪造——chi 的 RealIP 正是因此被官方弃用
// （GHSA-3fxj-6jh8-hvhx 等），本项目不再使用它。
type TrustedProxies struct {
	// Count > 0：信任固定层数的代理，取 X-Forwarded-For 从右往左第 Count 个。
	Count int
	// CIDRs 非空：从右往左跳过落在这些网段里的地址，第一个不在其中的就是客户端。
	CIDRs []string
}

// Direct 表示没有可信代理，客户端 IP 只认 TCP 连接地址。
func (t TrustedProxies) Direct() bool { return t.Count == 0 && len(t.CIDRs) == 0 }

// Config 是服务端启动配置。
type Config struct {
	Listen         string         // VM_LISTEN，默认 ":9000"
	DataDir        string         // VM_DATA_DIR，默认 "./data"：放 vm.db、jwt.secret、corefiles/、agent/、backup/
	PublicURL      string         // VM_PUBLIC_URL，如 https://panel.example.com；为空时安装命令用请求的 Host 拼
	JWTSecret      string         // VM_JWT_SECRET，为空则首次启动生成并写入 {DataDir}/jwt.secret（0600）
	TZ             string         // VM_TZ，默认 Asia/Shanghai；结算类任务按这个时区算日历
	LogLevel       string         // VM_LOG_LEVEL，默认 info（debug / info / warn / error）
	TrustedProxies TrustedProxies // VM_TRUSTED_PROXIES，默认空 = 直连

	// AgentDist 是镜像自带的 agent 产物目录（VM_AGENT_DIST，镜像里是 /app/agent-dist）。
	// 启动时会把它同步到 {DataDir}/agent/ 供节点下载，这样「升级镜像 = 升级可下载的 agent」。
	// 本地开发不设这个变量，同步就整个跳过。
	AgentDist string
}

// Load 从环境变量读取配置并做基本校验。
func Load() (Config, error) {
	cfg := Config{
		Listen:    getenv("VM_LISTEN", ":9000"),
		DataDir:   getenv("VM_DATA_DIR", "./data"),
		PublicURL: strings.TrimRight(strings.TrimSpace(os.Getenv("VM_PUBLIC_URL")), "/"),
		JWTSecret: strings.TrimSpace(os.Getenv("VM_JWT_SECRET")),
		TZ:        getenv("VM_TZ", "Asia/Shanghai"),
		LogLevel:  getenv("VM_LOG_LEVEL", "info"),
		AgentDist: strings.TrimSpace(os.Getenv("VM_AGENT_DIST")),
	}

	if _, err := ParseLogLevel(cfg.LogLevel); err != nil {
		return cfg, err
	}
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return cfg, fmt.Errorf("VM_PUBLIC_URL 必须是 http(s):// 开头的完整地址，当前为 %q", cfg.PublicURL)
		}
	}
	if _, err := time.LoadLocation(cfg.TZ); err != nil {
		return cfg, fmt.Errorf("VM_TZ %q 无效: %w", cfg.TZ, err)
	}

	tp, err := ParseTrustedProxies(os.Getenv("VM_TRUSTED_PROXIES"))
	if err != nil {
		return cfg, err
	}
	cfg.TrustedProxies = tp

	return cfg, nil
}

// ParseTrustedProxies 解析 VM_TRUSTED_PROXIES：
//
//	""                          直连，不采信任何代理头（默认）
//	"1"                         前面有 1 层可信代理（典型：同机 Caddy）
//	"10.0.0.0/8,172.18.0.0/16"  按网段跳过可信代理
func ParseTrustedProxies(raw string) (TrustedProxies, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return TrustedProxies{}, nil
	}

	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 {
			return TrustedProxies{}, fmt.Errorf("VM_TRUSTED_PROXIES 为数字时必须 ≥ 1，当前为 %q；留空表示直连", s)
		}
		return TrustedProxies{Count: n}, nil
	}

	var cidrs []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, err := netip.ParsePrefix(part); err != nil {
			return TrustedProxies{}, fmt.Errorf(
				"VM_TRUSTED_PROXIES 里的 %q 不是合法 CIDR（形如 10.0.0.0/8）；整个值也可以写成代理层数，如 1: %w", part, err)
		}
		cidrs = append(cidrs, part)
	}
	if len(cidrs) == 0 {
		return TrustedProxies{}, fmt.Errorf("VM_TRUSTED_PROXIES = %q 解析不出任何 CIDR", s)
	}
	return TrustedProxies{CIDRs: cidrs}, nil
}

// Location 返回 TZ 对应的时区。
func (c Config) Location() (*time.Location, error) {
	loc, err := time.LoadLocation(c.TZ)
	if err != nil {
		return nil, fmt.Errorf("VM_TZ %q 无效: %w", c.TZ, err)
	}
	return loc, nil
}

// ParseLogLevel 把 debug / info / warn / error 解析成 slog.Level。
func ParseLogLevel(s string) (slog.Level, error) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo, fmt.Errorf("VM_LOG_LEVEL %q 无效（可选 debug / info / warn / error）", s)
	}
	return lv, nil
}

// AgentDir 返回节点下载 agent 的目录：{DataDir}/agent。
func (c Config) AgentDir() string {
	return filepath.Join(c.DataDir, "agent")
}

// BackupDir 返回备份目录：{DataDir}/backup。
func (c Config) BackupDir() string {
	return filepath.Join(c.DataDir, "backup")
}

// DBPath 返回 SQLite 文件路径：{DataDir}/vm.db。
func (c Config) DBPath() string {
	return filepath.Join(c.DataDir, "vm.db")
}

// EnsureJWTSecret 决定 JWT 密钥：VM_JWT_SECRET 非空就直接用；否则读 {DataDir}/jwt.secret，
// 文件不存在时生成 32 字节随机数（hex 编码）并以 0600 写入。
func EnsureJWTSecret(cfg Config) (string, error) {
	if cfg.JWTSecret != "" {
		return cfg.JWTSecret, nil
	}
	if err := os.MkdirAll(cfg.DataDir, DataDirPerm); err != nil {
		return "", fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}
	path := filepath.Join(cfg.DataDir, JWTSecretFile)

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		s := strings.TrimSpace(string(b))
		if s == "" {
			return "", fmt.Errorf("%s 内容为空：删掉它重启会重新生成（已签发的登录态会全部失效）", path)
		}
		return s, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("读取 %s: %w", path, err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成随机密钥: %w", err)
	}
	s := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(s+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入 %s: %w", path, err)
	}
	slog.Info("jwt secret generated", "file", path)
	return s, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
