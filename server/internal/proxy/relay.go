package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"vpsmon/server/internal/proxy/keys"
	"vpsmon/server/internal/proxy/sub"
	"vpsmon/server/internal/store"
)

type relaySnapshot struct {
	source, target store.RenderData
	inbound        *store.Inbound
	subscriber     *store.Subscriber
	host, name     string
}

func loadRelay(ctx context.Context, q store.ProxyQueries, sourceID, targetID, inboundID int64) (relaySnapshot, error) {
	var r relaySnapshot
	var err error
	if sourceID <= 0 || targetID <= 0 || sourceID == targetID {
		return r, invalid(fmt.Errorf("中转需要两个不同节点"))
	}
	r.source, err = q.RenderData(ctx, sourceID)
	if err != nil {
		return r, err
	}
	r.target, err = q.RenderData(ctx, targetID)
	if err != nil {
		return r, err
	}
	r.inbound, err = q.Inbound(ctx, inboundID)
	if err != nil {
		return r, err
	}
	if r.inbound.ServerID != targetID || !r.inbound.Enabled || (r.inbound.Protocol != "vless" && r.inbound.Protocol != "shadowsocks") {
		return r, invalid(fmt.Errorf("目标必须为所选节点已启用的 VLESS 或 Shadowsocks 入站"))
	}
	err = q.DB.QueryRowContext(ctx, "SELECT name,public_host FROM servers WHERE id=?", targetID).Scan(&r.name, &r.host)
	if err != nil {
		return r, err
	}
	if r.host == "" {
		return r, invalid(fmt.Errorf("目标节点必须填写公开地址"))
	}
	var id int64
	err = q.DB.QueryRowContext(ctx, "SELECT id FROM subscribers WHERE kind='relay' AND name=?", fmt.Sprintf("relay:%d->%d", sourceID, targetID)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.subscriber, err = q.Subscriber(ctx, id)
	return r, err
}
func relayHash(r relaySnapshot) string {
	// Traffic counters are intentionally excluded; credentials/assignment and both render inputs are included.
	credential := ""
	if r.subscriber != nil {
		b, _ := json.Marshal(struct {
			ID                    int64
			UUID, Password, SSKey string
		}{r.subscriber.ID, r.subscriber.UUID, r.subscriber.Password, r.subscriber.SSUserKey})
		credential = string(b)
	}
	return inputHash(r.source) + inputHash(r.target) + r.host + r.name + credential
}
func mergeRelay(raw json.RawMessage, outbound map[string]any) (json.RawMessage, error) {
	var extra map[string]any
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, err
	}
	if extra == nil {
		extra = map[string]any{}
	}
	list := []any{}
	if old, ok := extra["outbounds"]; ok {
		var valid bool
		list, valid = old.([]any)
		if !valid {
			return nil, invalid(fmt.Errorf("outbounds 必须是数组"))
		}
	}
	tag := outbound["tag"]
	replaced := false
	for i, item := range list {
		if entry, ok := item.(map[string]any); ok && entry["tag"] == tag {
			list[i] = outbound
			replaced = true
		}
	}
	if !replaced {
		list = append(list, outbound)
	}
	extra["outbounds"] = list
	route := map[string]any{}
	if old, ok := extra["route"]; ok {
		var valid bool
		route, valid = old.(map[string]any)
		if !valid {
			return nil, invalid(fmt.Errorf("route 必须是对象"))
		}
	}
	route["final"] = tag
	extra["route"] = route
	return json.Marshal(extra)
}
func (s *Service) Relay(ctx context.Context, sourceID, targetID, inboundID int64, checker AdvancedChecker) (*store.Advanced, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	snap, err := loadRelay(ctx, store.ProxyQueries{DB: tx}, sourceID, targetID, inboundID)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		return nil, 0, err
	}
	subscriber := snap.subscriber
	if subscriber == nil {
		now := s.now().Unix()
		subscriber = &store.Subscriber{Name: fmt.Sprintf("relay:%d->%d", sourceID, targetID), Kind: "relay", SubToken: keys.SubToken(), CreatedAt: now, PeriodStart: now}
		credentials(subscriber)
	} else {
		copy := *subscriber
		subscriber = &copy
	}
	subscriber.Enabled = true
	subscriber.AutoDisabled = "none"
	subscriber.TrafficLimit = 0
	subscriber.ExpireAt = nil
	subscriber.ResetDay = 0
	subscriber.UpdatedAt = s.now().Unix()
	outbound, err := sub.SingboxOutbound(sub.Proxy{Name: fmt.Sprintf("relay-%s-%d", snap.name, targetID), Host: snap.host, Port: snap.inbound.ListenPort, Protocol: snap.inbound.Protocol, Inbound: *snap.inbound, Sub: *subscriber})
	if err != nil {
		return nil, 0, err
	}
	raw, err := mergeRelay(snap.source.Extra, outbound)
	if err != nil {
		return nil, 0, err
	}
	compact, err := preflight(ctx, snap.source, raw, checker)
	if err != nil {
		return nil, 0, err
	}
	var advanced *store.Advanced
	err = s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		fresh, err := loadRelay(ctx, q, sourceID, targetID, inboundID)
		if err != nil {
			return err
		}
		if relayHash(fresh) != relayHash(snap) {
			return ErrAdvancedChanged
		}
		if err = q.SaveSubscriber(ctx, subscriber); err != nil {
			return err
		}
		if _, err = q.DB.ExecContext(ctx, "UPDATE subscribers SET kind='relay' WHERE id=?", subscriber.ID); err != nil {
			return err
		}
		if err = q.Assign(ctx, subscriber.ID, []int64{inboundID}); err != nil {
			return err
		}
		now := s.now().Unix()
		advanced = &store.Advanced{ServerID: sourceID, ExtraJSON: compact, UpdatedAt: &now}
		if err = q.SaveAdvanced(ctx, advanced); err != nil {
			return err
		}
		return s.record(ctx, q, "node_advanced.relay", "server", sourceID, nil, map[string]any{"target_server_id": targetID, "target_inbound_id": inboundID, "relay_subscriber_id": subscriber.ID})
	})
	if err == nil {
		s.notify([]int64{sourceID, targetID}, "node_advanced.relay")
	}
	return advanced, subscriber.ID, err
}

// RemoveRelay removes only the active helper outbound. Dedicated users remain
// for historical traffic but are unassigned, so the target stops accepting them.
func (s *Service) RemoveRelay(ctx context.Context, sourceID int64, checker AdvancedChecker) (*store.Advanced, error) {
	data, err := s.advancedSnapshot(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	var extra map[string]any
	_ = json.Unmarshal(data.Extra, &extra)
	route, _ := extra["route"].(map[string]any)
	tag, _ := route["final"].(string)
	if !strings.HasPrefix(tag, "relay-") {
		return nil, invalid(fmt.Errorf("当前默认出口不是助手中转"))
	}
	outbounds, _ := extra["outbounds"].([]any)
	kept := []any{}
	for _, item := range outbounds {
		outbound, _ := item.(map[string]any)
		if outbound["tag"] != tag {
			kept = append(kept, item)
		}
	}
	extra["outbounds"] = kept
	delete(route, "final")
	raw, _ := json.Marshal(extra)
	compact, err := preflight(ctx, data, raw, checker)
	if err != nil {
		return nil, err
	}
	var result *store.Advanced
	changed := []int64{sourceID}
	err = s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		fresh, err := q.RenderData(ctx, sourceID)
		if err != nil {
			return err
		}
		if inputHash(fresh) != inputHash(data) {
			return ErrAdvancedChanged
		}
		all, err := q.Subscribers(ctx)
		if err != nil {
			return err
		}
		for _, subscriber := range all {
			if subscriber.Kind == "relay" && subscriber.Name == fmt.Sprintf("relay:%d->%s", sourceID, tag[strings.LastIndex(tag, "-")+1:]) {
				changed = append(changed, nodeIDs(subscriber)...)
				if err = q.Assign(ctx, subscriber.ID, []int64{}); err != nil {
					return err
				}
			}
		}
		now := s.now().Unix()
		result = &store.Advanced{ServerID: sourceID, ExtraJSON: compact, UpdatedAt: &now}
		if err = q.SaveAdvanced(ctx, result); err != nil {
			return err
		}
		return s.record(ctx, q, "node_advanced.remove_relay", "server", sourceID, nil, map[string]bool{"removed": true})
	})
	if err == nil {
		s.notify(changed, "node_advanced.remove_relay")
	}
	return result, err
}
