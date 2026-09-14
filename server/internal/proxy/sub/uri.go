package sub

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"vpsmon/server/internal/proxy/model"
)

func ProxyURI(p Proxy) (string, error) {
	settings, err := model.ParseSettings(p.Protocol, p.Inbound.Settings)
	if err != nil {
		return "", err
	}
	u := url.URL{Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), Fragment: p.Name}
	q := url.Values{}
	switch s := settings.(type) {
	case *model.VlessSettings:
		u.Scheme = "vless"
		u.User = url.User(p.Sub.UUID)
		q.Set("encryption", "none")
		q.Set("flow", "xtls-rprx-vision")
		q.Set("security", "reality")
		q.Set("sni", s.HandshakeServer)
		q.Set("fp", "chrome")
		q.Set("pbk", s.PublicKey)
		q.Set("sid", s.ShortIDs[0])
		q.Set("type", "tcp")
	case *model.ShadowsocksSettings:
		// SIP002 AEAD-2022 credentials are percent-encoded userinfo, not legacy base64 userinfo.
		u.Scheme = "ss"
		u.User = url.UserPassword(s.Method, s.ServerPSK+":"+p.Sub.SSUserKey)
	case *model.Hysteria2Settings:
		if p.Cert == nil {
			return "", fmt.Errorf("certificate missing")
		}
		u.Scheme = "hysteria2"
		u.User = url.User(p.Sub.Password)
		q.Set("sni", p.Cert.SNI)
		q.Set("insecure", "1")
		q.Set("pinSHA256", strings.ReplaceAll(p.Cert.FingerprintSHA256, ":", ""))
		if s.ObfsEnabled {
			q.Set("obfs", "salamander")
			q.Set("obfs-password", s.ObfsPassword)
		}
	case *model.TuicSettings:
		if p.Cert == nil {
			return "", fmt.Errorf("certificate missing")
		}
		u.Scheme = "tuic"
		u.User = url.UserPassword(p.Sub.UUID, p.Sub.Password)
		q.Set("congestion_control", s.CongestionControl)
		q.Set("alpn", "h3")
		q.Set("sni", p.Cert.SNI)
		q.Set("udp_relay_mode", "native")
		q.Set("allow_insecure", "1")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func RenderURI(proxies []Proxy) ([]byte, error) {
	lines := []string{}
	for _, p := range proxies {
		uri, err := ProxyURI(p)
		if err != nil {
			return nil, err
		}
		lines = append(lines, uri)
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))), nil
}
