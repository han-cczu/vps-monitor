package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vpsmon/server/internal/proxy/keys"
	"vpsmon/server/internal/store"
)

type SubscriberInput struct {
	Name         *string         `json:"name"`
	Note         *string         `json:"note"`
	Enabled      *bool           `json:"enabled"`
	TrafficLimit *int64          `json:"traffic_limit"`
	ResetDay     *int            `json:"reset_day"`
	ExpireAt     json.RawMessage `json:"expire_at"`
}

func credentials(s *store.Subscriber) {
	s.UUID = keys.UUID()
	s.Password = keys.Password24()
	s.SSUserKey = keys.PSK16()
}
func (s *Service) SaveSubscriber(ctx context.Context, id int64, in SubscriberInput) (*store.Subscriber, error) {
	var result *store.Subscriber
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		var before *store.Subscriber
		now := s.now()
		sub := store.Subscriber{Enabled: true, AutoDisabled: "none", CreatedAt: now.Unix(), AssignedInbounds: []store.Assignment{}}
		if id == 0 {
			sub.SubToken = keys.SubToken()
			credentials(&sub)
			sub.PeriodStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
		} else {
			var err error
			before, err = q.Subscriber(ctx, id)
			if err != nil {
				return err
			}
			sub = *before
			if sub.Kind == "relay" {
				return invalid(fmt.Errorf("中转专用用户由中转助手管理"))
			}
		}
		if in.Name != nil {
			sub.Name = strings.TrimSpace(*in.Name)
		}
		if sub.Name == "" {
			return invalid(fmt.Errorf("订阅用户名称不能为空"))
		}
		if err := checkText(sub.Name, 64); err != nil {
			return invalid(err)
		}
		if in.Note != nil {
			sub.Note = strings.TrimSpace(*in.Note)
		}
		if err := checkText(sub.Note, 2000); err != nil {
			return invalid(err)
		}
		if in.Enabled != nil {
			sub.Enabled = *in.Enabled
		}
		if in.TrafficLimit != nil {
			sub.TrafficLimit = *in.TrafficLimit
		}
		if in.ResetDay != nil {
			sub.ResetDay = *in.ResetDay
		}
		if sub.TrafficLimit < 0 || sub.TrafficLimit > 9007199254740991 || sub.ResetDay < 0 || sub.ResetDay > 31 {
			return invalid(fmt.Errorf("额度必须在 0–9007199254740991 字节，重置日为 0–31"))
		}
		if len(in.ExpireAt) > 0 {
			if bytes.Equal(bytes.TrimSpace(in.ExpireAt), []byte("null")) {
				sub.ExpireAt = nil
			} else {
				var date string
				if json.Unmarshal(in.ExpireAt, &date) != nil {
					return invalid(fmt.Errorf("expire_at 必须是 YYYY-MM-DD 或 null"))
				}
				if date == "" {
					sub.ExpireAt = nil
				} else {
					if _, err := time.Parse(time.DateOnly, date); err != nil {
						return invalid(fmt.Errorf("expire_at 日期不合法"))
					}
					sub.ExpireAt = &date
				}
			}
		}
		sub.UpdatedAt = now.Unix()
		if err := q.SaveSubscriber(ctx, &sub); err != nil {
			return err
		}
		action := "subscriber.update"
		if id == 0 {
			action = "subscriber.create"
		}
		if err := s.record(ctx, q, action, "subscriber", sub.ID, before, &sub); err != nil {
			return err
		}
		result = &sub
		return nil
	})
	if err == nil {
		s.notify(nodeIDs(result), "subscriber.changed")
	}
	return result, err
}
func (s *Service) DeleteSubscriber(ctx context.Context, id int64) error {
	var ids []int64
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		before, err := q.Subscriber(ctx, id)
		if err != nil {
			return err
		}
		ids = nodeIDs(before)
		if before.Kind == "relay" {
			return invalid(fmt.Errorf("请使用移除中转；中转用户保留历史分账"))
		}
		if err = q.DeleteSubscriber(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, q, "subscriber.delete", "subscriber", id, before, nil)
	})
	if err == nil {
		s.notify(ids, "subscriber.delete")
	}
	return err
}
func (s *Service) Assign(ctx context.Context, id int64, inboundIDs []int64) (*store.Subscriber, error) {
	if inboundIDs == nil {
		return nil, invalid(fmt.Errorf("inbound_ids 必须是数组；清空分配请传 []"))
	}
	if len(inboundIDs) > 1000 {
		return nil, invalid(fmt.Errorf("最多分配 1000 个入站"))
	}
	seen := map[int64]bool{}
	for _, id := range inboundIDs {
		if id <= 0 || seen[id] {
			return nil, invalid(fmt.Errorf("入站 ID 必须为正数且不重复"))
		}
		seen[id] = true
	}
	var result *store.Subscriber
	var changed []int64
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		before, err := q.Subscriber(ctx, id)
		if err != nil {
			return err
		}
		changed = nodeIDs(before)
		if before.Kind == "relay" {
			return invalid(fmt.Errorf("中转专用用户的分配由中转助手管理"))
		}
		for _, inboundID := range inboundIDs {
			if _, err = q.Inbound(ctx, inboundID); err != nil {
				return err
			}
		}
		if err = q.Assign(ctx, id, inboundIDs); err != nil {
			return err
		}
		result, err = q.Subscriber(ctx, id)
		if err != nil {
			return err
		}
		result.UpdatedAt = s.now().Unix()
		if err = q.SaveSubscriber(ctx, result); err != nil {
			return err
		}
		changed = append(changed, nodeIDs(result)...)
		return s.record(ctx, q, "assignment.update", "subscriber", id, before.AssignedInbounds, result.AssignedInbounds)
	})
	if err == nil {
		s.notify(changed, "assignment.update")
	}
	return result, err
}
func (s *Service) SubscriberAction(ctx context.Context, id int64, action string) (*store.Subscriber, error) {
	if action != "reset-token" && action != "regenerate-credentials" && action != "reset-usage" {
		return nil, invalid(fmt.Errorf("不支持的订阅用户操作"))
	}
	var result *store.Subscriber
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		before, err := q.Subscriber(ctx, id)
		if err != nil {
			return err
		}
		sub := *before
		if sub.Kind == "relay" && action != "reset-usage" {
			return invalid(fmt.Errorf("中转专用凭据由中转助手管理，不能单独旋转"))
		}
		switch action {
		case "reset-token":
			sub.SubToken = keys.SubToken()
		case "regenerate-credentials":
			credentials(&sub)
		case "reset-usage":
			sub.TrafficUsed = 0
			if sub.AutoDisabled == "quota" {
				sub.AutoDisabled = "none"
			}
			if err = q.ClearCurrentUsage(ctx, &sub); err != nil {
				return err
			}
		}
		sub.UpdatedAt = s.now().Unix()
		if err = q.SaveSubscriber(ctx, &sub); err != nil {
			return err
		}
		if err = s.record(ctx, q, "subscriber."+strings.ReplaceAll(action, "-", "_"), "subscriber", id, before, &sub); err != nil {
			return err
		}
		result = &sub
		return nil
	})
	if err == nil && action != "reset-token" {
		s.notify(nodeIDs(result), "subscriber."+action)
	}
	return result, err
}
