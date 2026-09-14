package sub

import (
	"encoding/json"
	"fmt"
	"strings"
	"vpsmon/server/internal/proxy/model"
)

func SingboxOutbound(p Proxy) (map[string]any, error) {
	settings, err := model.ParseSettings(p.Protocol, p.Inbound.Settings)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"type": p.Protocol, "tag": p.Name, "server": p.Host, "server_port": p.Port}
	switch s := settings.(type) {
	case *model.VlessSettings:
		out["uuid"], out["flow"] = p.Sub.UUID, "xtls-rprx-vision"
		out["tls"] = map[string]any{"enabled": true, "server_name": s.HandshakeServer, "utls": map[string]any{"enabled": true, "fingerprint": "chrome"}, "reality": map[string]any{"enabled": true, "public_key": s.PublicKey, "short_id": s.ShortIDs[0]}}
	case *model.ShadowsocksSettings:
		out["method"], out["password"] = s.Method, s.ServerPSK+":"+p.Sub.SSUserKey
	case *model.Hysteria2Settings:
		out["password"] = p.Sub.Password
		if s.ObfsEnabled {
			out["obfs"] = map[string]any{"type": "salamander", "password": s.ObfsPassword}
		}
		if s.UpMbps > 0 {
			out["up_mbps"] = s.UpMbps
		}
		if s.DownMbps > 0 {
			out["down_mbps"] = s.DownMbps
		}
	case *model.TuicSettings:
		out["uuid"], out["password"], out["congestion_control"], out["udp_relay_mode"] = p.Sub.UUID, p.Sub.Password, s.CongestionControl, "native"
		out["zero_rtt_handshake"] = s.ZeroRTT
	}
	if p.Protocol == "hysteria2" || p.Protocol == "tuic" {
		if p.Cert == nil || p.Cert.CertPEM == "" {
			return nil, fmt.Errorf("certificate missing")
		}
		out["tls"] = map[string]any{"enabled": true, "insecure": false, "server_name": p.Cert.SNI, "alpn": []string{"h3"}, "certificate": strings.Split(strings.TrimSpace(p.Cert.CertPEM), "\n")}
	}
	return out, nil
}
func RenderSingbox(proxies []Proxy) ([]byte, error) {
	out := []map[string]any{}
	for _, p := range proxies {
		v, err := SingboxOutbound(p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return json.MarshalIndent(map[string]any{"outbounds": out}, "", "  ")
}
