package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

type InboundInput struct {
	Protocol   string          `json:"protocol"`
	ListenPort *int            `json:"listen_port"`
	Settings   json.RawMessage `json:"settings"`
	Remark     *string         `json:"remark"`
	Enabled    *bool           `json:"enabled"`
}

func (s *Service) SaveInbound(ctx context.Context, serverID, id int64, in InboundInput) (*store.Inbound, error) {
	var result *store.Inbound
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		var before *store.Inbound
		now := s.now().Unix()
		i := store.Inbound{ServerID: serverID, Enabled: true, CreatedAt: now}
		if id != 0 {
			var err error
			before, err = q.Inbound(ctx, id)
			if err != nil {
				return err
			}
			i = *before
			serverID = i.ServerID
			if in.Protocol != "" && in.Protocol != i.Protocol {
				return invalid(fmt.Errorf("编辑入站不能更换协议"))
			}
		} else {
			i.Protocol = in.Protocol
		}
		if err := q.ServerExists(ctx, serverID); err != nil {
			return err
		}
		if err := s.guard(serverID); err != nil {
			return err
		}
		if in.ListenPort != nil {
			i.ListenPort = *in.ListenPort
		}
		if in.Remark != nil {
			i.Remark = strings.TrimSpace(*in.Remark)
		}
		if in.Enabled != nil {
			i.Enabled = *in.Enabled
		}
		settings, err := model.PrepareSettings(i.Protocol, in.Settings, i.Settings, false)
		if err != nil {
			return invalid(err)
		}
		i.Settings = settings
		if err = i.Validate(); err != nil {
			return invalid(err)
		}
		if err = checkText(i.Remark, 64); err != nil {
			return invalid(err)
		}
		i.Tag = model.Tag(i.Protocol, i.ListenPort)
		i.UpdatedAt = now
		others, err := q.Inbounds(ctx, i.ServerID)
		if err != nil {
			return err
		}
		for _, other := range others {
			if model.Conflicts(i, other) {
				return ErrConflict
			}
		}
		if err = q.SaveInbound(ctx, &i); err != nil {
			return conflictError(err)
		}
		if i.Protocol == "hysteria2" || i.Protocol == "tuic" {
			if _, err = q.Cert(ctx, i.ServerID); errors.Is(err, store.ErrNotFound) {
				c, err := certs.Generate("www.bing.com", s.now())
				if err != nil {
					return err
				}
				c.ServerID = i.ServerID
				if err = q.SaveCert(ctx, &c); err != nil {
					return err
				}
				if err = s.record(ctx, q, "cert.generate", "cert", i.ServerID, nil, &c); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
		action := "inbound.update"
		if id == 0 {
			action = "inbound.create"
		}
		if err = s.record(ctx, q, action, "inbound", i.ID, before, &i); err != nil {
			return err
		}
		result = &i
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.notify([]int64{serverID}, "inbound.changed")
	return result, nil
}
func (s *Service) DeleteInbound(ctx context.Context, id int64) error {
	var serverID int64
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		i, err := q.Inbound(ctx, id)
		if err != nil {
			return err
		}
		serverID = i.ServerID
		if err := s.guard(serverID); err != nil {
			return err
		}
		if err = q.DeleteInbound(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, q, "inbound.delete", "inbound", id, i, nil)
	})
	if err == nil {
		s.notify([]int64{serverID}, "inbound.delete")
	}
	return err
}
func (s *Service) RegenerateKeys(ctx context.Context, id int64) (*store.Inbound, error) {
	var result *store.Inbound
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		before, err := q.Inbound(ctx, id)
		if err != nil {
			return err
		}
		if err := s.guard(before.ServerID); err != nil {
			return err
		}
		if before.Protocol == "tuic" {
			return invalid(fmt.Errorf("TUIC 入站无独立密钥，请重生订阅用户凭据或节点证书"))
		}
		i := *before
		i.Settings, err = model.PrepareSettings(i.Protocol, nil, i.Settings, true)
		if err != nil {
			return invalid(err)
		}
		i.UpdatedAt = s.now().Unix()
		if err = q.SaveInbound(ctx, &i); err != nil {
			return err
		}
		if err = s.record(ctx, q, "inbound.regenerate_keys", "inbound", id, before, &i); err != nil {
			return err
		}
		result = &i
		return nil
	})
	if err == nil {
		s.notify([]int64{result.ServerID}, "inbound.regenerate_keys")
	}
	return result, err
}
func (s *Service) RegenerateCert(ctx context.Context, serverID int64, sni string) (*certs.Cert, error) {
	if err := s.guard(serverID); err != nil {
		return nil, err
	}
	var result *certs.Cert
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		if err := q.ServerExists(ctx, serverID); err != nil {
			return err
		}
		before, err := q.Cert(ctx, serverID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if errors.Is(err, store.ErrNotFound) {
			before = nil
		}
		if sni == "" {
			sni = "www.bing.com"
			if before != nil {
				sni = before.SNI
			}
		}
		if !certs.ValidSNI(sni) {
			return invalid(fmt.Errorf("SNI 必须是合法的 DNS 域名"))
		}
		c, err := certs.Generate(sni, s.now())
		if err != nil {
			return err
		}
		c.ServerID = serverID
		if err = q.SaveCert(ctx, &c); err != nil {
			return err
		}
		if err = s.record(ctx, q, "cert.regenerate", "cert", serverID, before, &c); err != nil {
			return err
		}
		result = &c
		return nil
	})
	if err == nil {
		s.notify([]int64{serverID}, "cert.regenerate")
	}
	return result, err
}
