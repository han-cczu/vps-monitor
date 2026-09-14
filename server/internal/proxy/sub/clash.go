package sub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"strconv"
	"strings"
	"vpsmon/server/internal/proxy/model"
)

const DefaultClashTemplate = `{{PROXIES}}
proxy-groups:
  - name: "代理选择"
    type: "select"
    proxies: [{{PROXY_NAMES}}, "DIRECT"]
rules:
  - "MATCH,代理选择"
`

func scalar(v any) *yaml.Node {
	switch v := v.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
	case *yaml.Node:
		return v
	case []string:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, s := range v {
			n.Content = append(n.Content, scalar(s))
		}
		return n
	default:
		panic("unsupported subscription scalar")
	}
}
func mapping(values ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(values); i += 2 {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: values[i].(string)}, scalar(values[i+1]))
	}
	return n
}
func clashProxy(p Proxy) (*yaml.Node, error) {
	settings, err := model.ParseSettings(p.Protocol, p.Inbound.Settings)
	if err != nil {
		return nil, err
	}
	kind := p.Protocol
	if kind == "shadowsocks" {
		kind = "ss"
	}
	n := mapping("name", p.Name, "type", kind, "server", p.Host, "port", p.Port, "udp", true)
	add := func(v ...any) { n.Content = append(n.Content, mapping(v...).Content...) }
	switch s := settings.(type) {
	case *model.VlessSettings:
		add("uuid", p.Sub.UUID, "flow", "xtls-rprx-vision", "network", "tcp", "tls", true, "servername", s.HandshakeServer, "client-fingerprint", "chrome", "reality-opts", mapping("public-key", s.PublicKey, "short-id", s.ShortIDs[0]))
	case *model.ShadowsocksSettings:
		add("cipher", s.Method, "password", s.ServerPSK+":"+p.Sub.SSUserKey)
	case *model.Hysteria2Settings:
		if p.Cert == nil {
			return nil, fmt.Errorf("certificate missing")
		}
		add("password", p.Sub.Password, "sni", p.Cert.SNI, "skip-cert-verify", false, "fingerprint", p.Cert.FingerprintSHA256, "alpn", []string{"h3"})
		if s.ObfsEnabled {
			add("obfs", "salamander", "obfs-password", s.ObfsPassword)
		}
		if s.UpMbps > 0 {
			add("up", strconv.Itoa(s.UpMbps)+" Mbps")
		}
		if s.DownMbps > 0 {
			add("down", strconv.Itoa(s.DownMbps)+" Mbps")
		}
	case *model.TuicSettings:
		if p.Cert == nil {
			return nil, fmt.Errorf("certificate missing")
		}
		add("uuid", p.Sub.UUID, "password", p.Sub.Password, "congestion-controller", s.CongestionControl, "udp-relay-mode", "native", "reduce-rtt", s.ZeroRTT, "sni", p.Cert.SNI, "skip-cert-verify", false, "fingerprint", p.Cert.FingerprintSHA256, "alpn", []string{"h3"})
	}
	return n, nil
}
func RenderClashProvider(proxies []Proxy) ([]byte, error) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, p := range proxies {
		n, err := clashProxy(p)
		if err != nil {
			return nil, err
		}
		seq.Content = append(seq.Content, n)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(mapping("proxies", seq)); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return out.Bytes(), nil
}
func RenderClash(proxies []Proxy, tpl string) ([]byte, error) {
	if tpl == "" {
		tpl = DefaultClashTemplate
	}
	if strings.Count(tpl, "{{PROXIES}}") != 1 || !strings.Contains(tpl, "{{PROXY_NAMES}}") {
		return nil, fmt.Errorf("模板必须含一次 {{PROXIES}} 和至少一次 {{PROXY_NAMES}}")
	}
	body, err := RenderClashProvider(proxies)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(proxies))
	for _, p := range proxies {
		names = append(names, strconv.Quote(p.Name))
	}
	// A valid list is required even with no assigned nodes. DIRECT is a group fallback, never a proxy entry.
	if len(names) == 0 {
		names = append(names, `"DIRECT"`)
	}
	rendered := strings.NewReplacer("{{PROXIES}}", strings.TrimSuffix(string(body), "\n"), "{{PROXY_NAMES}}", strings.Join(names, ", ")).Replace(tpl)
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	var doc map[string]any
	if err = dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("订阅模板 YAML 不合法")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("订阅模板必须是单个 YAML 文档")
	}
	seqValue, ok := doc["proxies"].([]any)
	if !ok || len(seqValue) != len(proxies) {
		return nil, fmt.Errorf("模板 proxies 必须保留生成的代理列表")
	}
	return []byte(rendered), nil
}
func ValidateClashTemplate(tpl string) error {
	if len(tpl) > 256<<10 {
		return fmt.Errorf("订阅模板不得超过 256 KiB")
	}
	_, err := RenderClash(nil, tpl)
	if err != nil {
		return err
	}
	// Validate with multiple quoted names too: a placeholder that is legal as
	// one scalar must not become broken YAML as soon as a user gets two nodes.
	settings := json.RawMessage(`{"method":"2022-blake3-aes-128-gcm","server_psk":"AAAAAAAAAAAAAAAAAAAAAA=="}`)
	sample := []Proxy{{Name: `校验 "节点"`, Protocol: "shadowsocks", Host: "2001:db8::1", Port: 8388, Inbound: model.Inbound{Settings: settings}}, {Name: "on", Protocol: "shadowsocks", Host: "example.com", Port: 8389, Inbound: model.Inbound{Settings: settings}}}
	_, err = RenderClash(sample, tpl)
	return err
}
