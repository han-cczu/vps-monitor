// Package render compiles panel data into a deterministic sing-box configuration.
package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

type Input struct {
	Server         store.Server
	Inbounds       []model.Inbound
	UsersByInbound map[int64][]store.Subscriber
	Cert           *certs.Cert
	Extra          json.RawMessage
	Version        string
}
type Output struct {
	Config      []byte
	SHA256      string
	Ports       []string
	UserNames   []string
	InboundTags []string
	Warnings    []string
}

func Render(in Input) (Output, error) {
	out := Output{Ports: []string{}, UserNames: []string{}, InboundTags: []string{}}
	inboundList := []any{}
	items := slices.Clone(in.Inbounds)
	slices.SortFunc(items, func(a, b model.Inbound) int { return compareID(a.ID, b.ID) })
	names, ports, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, i := range items {
		if !i.Enabled {
			continue
		}
		if i.ServerID != in.Server.ID || i.Tag != model.Tag(i.Protocol, i.ListenPort) {
			return out, fmt.Errorf("入站不属于节点或 tag 不合法")
		}
		if err := i.Validate(); err != nil {
			return out, err
		}
		if tags[i.Tag] {
			return out, fmt.Errorf("重复的入站 tag")
		}
		tags[i.Tag] = true
		for _, tr := range model.Transports(i.Protocol) {
			p := strconv.Itoa(i.ListenPort) + "/" + tr
			if ports[p] || p == "10085/tcp" {
				return out, fmt.Errorf("端口冲突（含受管统计端口 10085/tcp）")
			}
			ports[p] = true
			out.Ports = append(out.Ports, p)
		}
		users := []any{}
		subs := slices.Clone(in.UsersByInbound[i.ID])
		slices.SortFunc(subs, func(a, b store.Subscriber) int { return compareID(a.ID, b.ID) })
		seen := map[int64]bool{}
		for _, u := range subs {
			if !u.Enabled || u.AutoDisabled != "none" {
				continue
			}
			if u.ID <= 0 || seen[u.ID] {
				return out, fmt.Errorf("用户 ID 非法或重复")
			}
			seen[u.ID] = true
			name := "sub-" + strconv.FormatInt(u.ID, 10)
			names[name] = true
			user := map[string]any{"name": name}
			switch i.Protocol {
			case "vless":
				user["uuid"] = u.UUID
				user["flow"] = "xtls-rprx-vision"
			case "shadowsocks":
				user["password"] = u.SSUserKey
			case "hysteria2":
				user["password"] = u.Password
			case "tuic":
				user["uuid"] = u.UUID
				user["password"] = u.Password
			}
			users = append(users, user)
		}
		b := map[string]any{"type": i.Protocol, "tag": i.Tag, "listen": "::", "listen_port": i.ListenPort, "users": users}
		settings, err := model.ParseSettings(i.Protocol, i.Settings)
		if err != nil {
			return out, err
		}
		switch s := settings.(type) {
		case *model.VlessSettings:
			b["tls"] = map[string]any{"enabled": true, "server_name": s.HandshakeServer, "reality": map[string]any{"enabled": true, "handshake": map[string]any{"server": s.HandshakeServer, "server_port": s.HandshakePort}, "private_key": s.PrivateKey, "short_id": s.ShortIDs, "max_time_difference": "1m"}}
		case *model.ShadowsocksSettings:
			b["method"], b["password"] = s.Method, s.ServerPSK
			// sing-box 1.14 otherwise treats users:[] as a single-user server and
			// accepts the shared server PSK. Managed mode keeps an empty multi-user ACL.
			if len(users) == 0 {
				b["managed"] = true
			}
		case *model.Hysteria2Settings:
			b["up_mbps"], b["down_mbps"], b["ignore_client_bandwidth"] = s.UpMbps, s.DownMbps, s.IgnoreClientBandwidth
			if s.ObfsEnabled {
				b["obfs"] = map[string]any{"type": "salamander", "password": s.ObfsPassword}
			}
		case *model.TuicSettings:
			b["congestion_control"], b["zero_rtt_handshake"], b["auth_timeout"], b["heartbeat"] = s.CongestionControl, s.ZeroRTT, "3s", "10s"
		}
		if i.Protocol == "hysteria2" || i.Protocol == "tuic" {
			if in.Cert == nil || in.Cert.ServerID != in.Server.ID || in.Cert.CertPEM == "" || in.Cert.KeyPEM == "" {
				return out, fmt.Errorf("Hy2/TUIC 缺少节点证书")
			}
			b["tls"] = map[string]any{"enabled": true, "alpn": []string{"h3"}, "certificate": pemLines(in.Cert.CertPEM), "key": pemLines(in.Cert.KeyPEM)}
		}
		inboundList = append(inboundList, b)
		out.InboundTags = append(out.InboundTags, i.Tag)
	}
	for name := range names {
		out.UserNames = append(out.UserNames, name)
	}
	slices.Sort(out.UserNames)
	slices.Sort(out.Ports)
	cfg := map[string]any{
		// systemd owns the log file; check must not open a production log path.
		"log":          map[string]any{"level": "warn", "timestamp": true},
		"experimental": map[string]any{"v2ray_api": map[string]any{"listen": "127.0.0.1:10085", "stats": map[string]any{"enabled": true, "inbounds": out.InboundTags, "users": out.UserNames}}},
		"inbounds":     inboundList, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{"rules": []any{map[string]any{"action": "sniff"}}, "final": "direct"},
	}
	var err error
	out.Warnings, err = merge(cfg, in.Extra)
	if err != nil {
		return out, err
	}
	out.Config, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return out, err
	}
	out.SHA256, err = Hash(out.Config)
	if len(out.Ports) > 1024 {
		return out, fmt.Errorf("入站监听项超过 1024")
	}
	return out, err
}
func compareID(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func pemLines(s string) []string {
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")), "\n")
}

// Hash matches the Agent's compact-JSON hash, independent of indentation.
func Hash(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) >= 1<<20 {
		return "", fmt.Errorf("配置必须小于 1 MiB")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil || compact.Bytes()[0] != '{' {
		return "", fmt.Errorf("配置必须是 JSON 对象")
	}
	sum := sha256.Sum256(compact.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// Inspect returns identities and ports from a persisted configuration, including
// rollback revisions. Only this renderer's four inbound types are accepted.
func Inspect(raw []byte) (Output, error) {
	out := Output{Config: raw, Ports: []string{}, UserNames: []string{}, InboundTags: []string{}}
	var err error
	out.SHA256, err = Hash(raw)
	if err != nil {
		return out, err
	}
	var cfg struct {
		Inbounds []struct {
			Type  string `json:"type"`
			Tag   string `json:"tag"`
			Port  int    `json:"listen_port"`
			Users []struct {
				Name string `json:"name"`
			} `json:"users"`
		} `json:"inbounds"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return out, fmt.Errorf("修订配置无效")
	}
	names, ports := map[string]bool{}, map[string]bool{}
	for _, i := range cfg.Inbounds {
		ts := model.Transports(i.Type)
		if len(ts) == 0 || i.Port < 1 || i.Port > 65535 {
			return out, fmt.Errorf("修订入站无效")
		}
		out.InboundTags = append(out.InboundTags, i.Tag)
		for _, tr := range ts {
			p := strconv.Itoa(i.Port) + "/" + tr
			if ports[p] || p == "10085/tcp" {
				return out, fmt.Errorf("修订端口冲突")
			}
			ports[p] = true
			out.Ports = append(out.Ports, p)
		}
		for _, u := range i.Users {
			names[u.Name] = true
		}
	}
	for name := range names {
		out.UserNames = append(out.UserNames, name)
	}
	slices.Sort(out.UserNames)
	slices.Sort(out.Ports)
	return out, nil
}
