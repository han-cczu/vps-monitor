package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"vpsmon/server/internal/proxy/model"
	"vpsmon/server/internal/store"
)

func validateAdvanced(raw json.RawMessage) (json.RawMessage, error) {
	obj, err := model.Object(raw, 256<<10)
	if err != nil {
		return nil, invalid(err)
	}
	for _, reserved := range []string{"inbounds", "experimental", "log"} {
		if _, ok := obj[reserved]; ok {
			return nil, invalid(fmt.Errorf("高级配置不能覆盖受管的 %s", reserved))
		}
	}
	if out, ok := obj["outbounds"]; ok {
		var items []json.RawMessage
		if len(bytes.TrimSpace(out)) == 0 || bytes.TrimSpace(out)[0] != '[' || json.Unmarshal(out, &items) != nil || len(items) > 1000 {
			return nil, invalid(fmt.Errorf("outbounds 必须是最多 1000 项的数组"))
		}
		tags := map[string]bool{"direct": true}
		for _, item := range items {
			v, err := model.Object(item, 256<<10)
			if err != nil {
				return nil, invalid(err)
			}
			var tag, kind string
			_ = json.Unmarshal(v["tag"], &tag)
			_ = json.Unmarshal(v["type"], &kind)
			if tag == "" || kind == "" || len(tag) > 128 || tags[tag] {
				return nil, invalid(fmt.Errorf("高级 outbound 必须含 type 和唯一 tag，不能使用 direct"))
			}
			tags[tag] = true
		}
	}
	if route, ok := obj["route"]; ok {
		r, err := model.Object(route, 256<<10)
		if err != nil {
			return nil, invalid(err)
		}
		if rules, ok := r["rules"]; ok {
			var items []json.RawMessage
			if len(bytes.TrimSpace(rules)) == 0 || bytes.TrimSpace(rules)[0] != '[' || json.Unmarshal(rules, &items) != nil {
				return nil, invalid(fmt.Errorf("route.rules 必须是数组"))
			}
			for _, item := range items {
				if _, err = model.Object(item, 256<<10); err != nil {
					return nil, invalid(err)
				}
			}
		}
		if final, ok := r["final"]; ok {
			var name string
			if json.Unmarshal(final, &name) != nil || name == "" {
				return nil, invalid(fmt.Errorf("route.final 必须是非空字符串"))
			}
		}
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, raw); err != nil {
		return nil, invalid(err)
	}
	return compact.Bytes(), nil
}
func (s *Service) SaveAdvanced(ctx context.Context, serverID int64, raw json.RawMessage) (*store.Advanced, error) {
	compact, err := validateAdvanced(raw)
	if err != nil {
		return nil, err
	}
	var result *store.Advanced
	err = s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		if err := q.ServerExists(ctx, serverID); err != nil {
			return err
		}
		before, err := q.Advanced(ctx, serverID)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		a := store.Advanced{ServerID: serverID, ExtraJSON: compact, UpdatedAt: &now}
		if err = q.SaveAdvanced(ctx, &a); err != nil {
			return err
		}
		if err = s.record(ctx, q, "node_advanced.update", "server", serverID, before, &a); err != nil {
			return err
		}
		result = &a
		return nil
	})
	if err == nil {
		s.notify([]int64{serverID}, "node_advanced.update")
	}
	return result, err
}
