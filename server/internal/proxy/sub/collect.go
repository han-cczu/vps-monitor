// Package sub renders client subscriptions; it never exports server private keys.
package sub

import (
	"context"
	"fmt"
	"strings"
	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

type Proxy struct {
	Name, ServerName, Protocol, Host string
	Port                             int
	Inbound                          model.Inbound
	Sub                              store.Subscriber
	Cert                             *certs.Cert
}

func DisabledReason(s store.Subscriber) string {
	if !s.Enabled {
		return "手动停用"
	}
	switch s.AutoDisabled {
	case "quota":
		return "超额停用"
	case "expired":
		return "到期停用"
	case "", "none":
		return ""
	default:
		return "已停用"
	}
}

func Collect(ctx context.Context, db *store.DB, subscriber store.Subscriber) ([]Proxy, error) {
	result := []Proxy{}
	if DisabledReason(subscriber) != "" {
		return result, nil
	}
	labels := map[string]string{"vless": "VLESS", "shadowsocks": "SS", "hysteria2": "HY2", "tuic": "TUIC"}
	counts := map[string]int{}
	for _, assignment := range subscriber.AssignedInbounds {
		inbound, err := db.Proxy().Inbound(ctx, assignment.InboundID)
		if err != nil {
			return nil, err
		}
		if !inbound.Enabled {
			continue
		}
		server, err := db.GetServer(ctx, inbound.ServerID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(server.PublicHost) == "" {
			continue
		}
		p := Proxy{Name: server.Name + " · " + labels[inbound.Protocol], ServerName: server.Name, Protocol: inbound.Protocol, Host: server.PublicHost, Port: inbound.ListenPort, Inbound: *inbound, Sub: subscriber}
		if inbound.Protocol == "hysteria2" || inbound.Protocol == "tuic" {
			p.Cert, err = db.Proxy().Cert(ctx, server.ID)
			if err != nil {
				return nil, err
			}
		}
		counts[p.Name]++
		result = append(result, p)
	}
	used := map[string]bool{}
	for i := range result {
		p := &result[i]
		if counts[p.Name] > 1 {
			p.Name += fmt.Sprintf(" #%d", p.Port)
		}
		if used[p.Name] {
			p.Name += fmt.Sprintf(" (节点 %d/入站 %d)", p.Inbound.ServerID, p.Inbound.ID)
		}
		used[p.Name] = true
	}
	return result, nil
}
