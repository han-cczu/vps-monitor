package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"vpsmon/server/internal/proxy/render"
	"vpsmon/server/internal/store"
)

type AdvancedChecker func(context.Context, string, []byte) error

var ErrAdvancedChanged = fmt.Errorf("预检期间节点配置发生变化，请重新校验后保存")

func (s *Service) advancedSnapshot(ctx context.Context, id int64) (store.RenderData, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.RenderData{}, err
	}
	defer tx.Rollback()
	data, err := (store.ProxyQueries{DB: tx}).RenderData(ctx, id)
	if err == nil {
		err = tx.Commit()
	}
	return data, err
}
func preflight(ctx context.Context, data store.RenderData, raw json.RawMessage, checker AdvancedChecker) (json.RawMessage, error) {
	compact, err := validateAdvanced(raw)
	if err != nil {
		return nil, err
	}
	data.Extra = compact
	rendered, err := build(data)
	if err != nil {
		return nil, invalid(err)
	}
	if data.Version == "" {
		return nil, invalid(ErrNoCurrentCore)
	}
	if checker == nil {
		return nil, invalid(fmt.Errorf("没有本地可执行核心，无法预检；未保存配置"))
	}
	if err = checker(ctx, data.Version, rendered.Config); err != nil {
		if errors.Is(err, render.ErrNoLocalCore) {
			return nil, invalid(fmt.Errorf("本地核心不可执行，无法预检；配置未保存"))
		}
		return nil, invalid(err)
	}
	return compact, nil
}
func (s *Service) CheckAdvanced(ctx context.Context, id int64, raw json.RawMessage, checker AdvancedChecker) error {
	data, err := s.advancedSnapshot(ctx, id)
	if err != nil {
		return err
	}
	_, err = preflight(ctx, data, raw, checker)
	return err
}
func (s *Service) SaveAdvancedChecked(ctx context.Context, id int64, raw json.RawMessage, checker AdvancedChecker) (*store.Advanced, error) {
	data, err := s.advancedSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	compact, err := preflight(ctx, data, raw, checker)
	if err != nil {
		return nil, err
	}
	var result *store.Advanced
	err = s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		current, err := q.RenderData(ctx, id)
		if err != nil {
			return err
		}
		if inputHash(current) != inputHash(data) {
			return ErrAdvancedChanged
		}
		before, err := q.Advanced(ctx, id)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		result = &store.Advanced{ServerID: id, ExtraJSON: compact, UpdatedAt: &now}
		if err = q.SaveAdvanced(ctx, result); err != nil {
			return err
		}
		return s.record(ctx, q, "node_advanced.update", "server", id, before, result)
	})
	if err == nil {
		s.notify([]int64{id}, "node_advanced.update")
	}
	return result, err
}
