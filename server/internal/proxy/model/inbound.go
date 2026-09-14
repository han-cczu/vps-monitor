package model

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/keys"
)

type Inbound struct {
	ID         int64           `json:"id"`
	ServerID   int64           `json:"server_id"`
	Tag        string          `json:"tag"`
	Protocol   string          `json:"protocol"`
	ListenPort int             `json:"listen_port"`
	Settings   json.RawMessage `json:"settings"`
	Remark     string          `json:"remark"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}
type VlessSettings struct {
	HandshakeServer string   `json:"handshake_server"`
	HandshakePort   int      `json:"handshake_port"`
	PrivateKey      string   `json:"private_key"`
	PublicKey       string   `json:"public_key"`
	ShortIDs        []string `json:"short_ids"`
}
type ShadowsocksSettings struct {
	Method    string `json:"method"`
	ServerPSK string `json:"server_psk"`
}
type Hysteria2Settings struct {
	ObfsEnabled           bool   `json:"obfs_enabled"`
	ObfsPassword          string `json:"obfs_password"`
	UpMbps                int    `json:"up_mbps"`
	DownMbps              int    `json:"down_mbps"`
	IgnoreClientBandwidth bool   `json:"ignore_client_bandwidth"`
}
type TuicSettings struct {
	CongestionControl string `json:"congestion_control"`
	ZeroRTT           bool   `json:"zero_rtt"`
}

func StrictJSON(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("JSON 字段或类型不合法")
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("JSON 包含多余内容")
	}
	return nil
}
func Object(raw []byte, max int) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(raw) > max || trimmed[0] != '{' {
		return nil, fmt.Errorf("需要大小受限的 JSON 对象")
	}
	var obj map[string]json.RawMessage
	if err := StrictJSON(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}
func ParseSettings(protocol string, raw json.RawMessage) (any, error) {
	if _, err := Object(raw, 64<<10); err != nil {
		return nil, err
	}
	var dst any
	switch protocol {
	case "vless":
		dst = &VlessSettings{}
	case "shadowsocks":
		dst = &ShadowsocksSettings{}
	case "hysteria2":
		dst = &Hysteria2Settings{}
	case "tuic":
		dst = &TuicSettings{}
	default:
		return nil, fmt.Errorf("不支持的代理协议")
	}
	if err := StrictJSON(raw, dst); err != nil {
		return nil, err
	}
	switch s := dst.(type) {
	case *VlessSettings:
		if (!certs.ValidSNI(s.HandshakeServer) && net.ParseIP(s.HandshakeServer) == nil) || s.HandshakePort < 1 || s.HandshakePort > 65535 {
			return nil, fmt.Errorf("Reality 握手目标或端口不合法")
		}
		pub, err := keys.PublicKey(s.PrivateKey)
		if err != nil {
			return nil, err
		}
		if s.PublicKey != pub {
			return nil, fmt.Errorf("Reality 公私钥不匹配")
		}
		if len(s.ShortIDs) < 1 || len(s.ShortIDs) > 16 {
			return nil, fmt.Errorf("short_ids 必须有 1–16 项")
		}
		seen := map[string]bool{}
		for _, id := range s.ShortIDs {
			b, err := hex.DecodeString(id)
			if err != nil || len(b) < 1 || len(b) > 8 || strings.ToLower(id) != id || seen[id] {
				return nil, fmt.Errorf("short_id 必须为 2–16 位偶数长度的小写 hex，且不重复")
			}
			seen[id] = true
		}
	case *ShadowsocksSettings:
		b, err := base64.StdEncoding.DecodeString(s.ServerPSK)
		if s.Method != "2022-blake3-aes-128-gcm" || err != nil || len(b) != 16 || base64.StdEncoding.EncodeToString(b) != s.ServerPSK {
			return nil, fmt.Errorf("SS2022 方法固定 aes-128，PSK 必须为 16 字节标准 base64")
		}
	case *Hysteria2Settings:
		if s.UpMbps < 0 || s.UpMbps > 1000000 || s.DownMbps < 0 || s.DownMbps > 1000000 {
			return nil, fmt.Errorf("带宽必须在 0–1000000 Mbps")
		}
		if (s.ObfsEnabled && s.ObfsPassword == "") || len(s.ObfsPassword) > 128 || strings.ContainsAny(s.ObfsPassword, "\x00\r\n") {
			return nil, fmt.Errorf("混淆密码不合法")
		}
	case *TuicSettings:
		if !slices.Contains([]string{"bbr", "cubic", "new_reno"}, s.CongestionControl) {
			return nil, fmt.Errorf("TUIC 拥塞控制必须为 bbr/cubic/new_reno")
		}
	}
	return dst, nil
}

// PrepareSettings merges a partial update without rotating omitted credentials.
func PrepareSettings(protocol string, raw, previous json.RawMessage, regenerate bool) (json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	if len(previous) > 0 {
		if err := json.Unmarshal(previous, &values); err != nil {
			return nil, err
		}
	}
	provided := map[string]json.RawMessage{}
	if len(raw) > 0 {
		var err error
		provided, err = Object(raw, 64<<10)
		if err != nil {
			return nil, err
		}
		for k, v := range provided {
			if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return nil, fmt.Errorf("settings 字段不能为 null")
			}
			values[k] = v
		}
	}
	put := func(k string, v any) { values[k], _ = json.Marshal(v) }
	missing := func(k string) bool { _, ok := values[k]; return !ok }
	switch protocol {
	case "vless":
		if missing("handshake_server") {
			put("handshake_server", "www.microsoft.com")
		}
		if missing("handshake_port") {
			put("handshake_port", 443)
		}
		if regenerate || missing("private_key") {
			if !regenerate && !missing("public_key") {
				return nil, fmt.Errorf("提供公钥时也必须提供私钥")
			}
			priv, pub := keys.X25519()
			put("private_key", priv)
			put("public_key", pub)
		} else if _, changed := provided["private_key"]; changed || missing("public_key") {
			if _, explicit := provided["public_key"]; !explicit {
				var priv string
				_ = json.Unmarshal(values["private_key"], &priv)
				pub, err := keys.PublicKey(priv)
				if err != nil {
					return nil, err
				}
				put("public_key", pub)
			}
		}
		if regenerate || missing("short_ids") {
			put("short_ids", []string{keys.ShortID()})
		}
	case "shadowsocks":
		if missing("method") {
			put("method", "2022-blake3-aes-128-gcm")
		}
		if regenerate || missing("server_psk") {
			put("server_psk", keys.PSK16())
		}
	case "hysteria2":
		if regenerate || missing("obfs_password") {
			put("obfs_password", keys.Password24())
		}
	case "tuic":
		if missing("congestion_control") {
			put("congestion_control", "bbr")
		}
	default:
		return nil, fmt.Errorf("不支持的代理协议")
	}
	b, _ := json.Marshal(values)
	parsed, err := ParseSettings(protocol, b)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

func (i *Inbound) Validate() error {
	if i.ServerID <= 0 || i.ListenPort < 1 || i.ListenPort > 65535 {
		return fmt.Errorf("节点 ID 或端口不合法")
	}
	if len([]rune(i.Remark)) > 64 {
		return fmt.Errorf("备注最多 64 个字")
	}
	_, err := ParseSettings(i.Protocol, i.Settings)
	return err
}
func Tag(protocol string, port int) string {
	prefix := map[string]string{"vless": "vless", "shadowsocks": "ss", "hysteria2": "hy2", "tuic": "tuic"}[protocol]
	return fmt.Sprintf("%s-%d", prefix, port)
}
func Transports(protocol string) []string {
	switch protocol {
	case "vless":
		return []string{"tcp"}
	case "shadowsocks":
		return []string{"tcp", "udp"}
	case "hysteria2", "tuic":
		return []string{"udp"}
	}
	return nil
}
func Conflicts(a, b Inbound) bool {
	if a.ServerID != b.ServerID || a.ListenPort != b.ListenPort || a.ID != 0 && a.ID == b.ID {
		return false
	}
	for _, p := range Transports(a.Protocol) {
		if slices.Contains(Transports(b.Protocol), p) {
			return true
		}
	}
	return false
}
