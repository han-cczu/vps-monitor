package alert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"vpsmon/server/internal/store"
)

type Params struct {
	Minutes  int     `json:"minutes,omitempty"`
	Percent  float64 `json:"percent,omitempty"`
	Percents []int   `json:"percents,omitempty"`
	Days     []int   `json:"days,omitempty"`
}

var Kinds = map[string]string{
	"server.offline": "节点离线", "server.cpu": "CPU 持续过高", "server.mem": "内存持续过高", "server.disk": "磁盘持续过高",
	"server.traffic": "节点流量阈值", "server.expire": "节点即将到期", "ping.loss": "Ping 丢包过高",
	"subscriber.quota": "订阅用量阈值", "subscriber.expired": "订阅用户到期", "core.apply_failed": "核心应用失败", "core.down": "核心停止运行",
}

func ParseParams(r store.AlertRule) (Params, error) {
	var p Params
	if _, ok := Kinds[r.Kind]; !ok {
		return p, errors.New("未知规则 kind")
	}
	dec := json.NewDecoder(bytes.NewReader(r.Params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, errors.New("规则 params 格式无效")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(r.Params, &fields); err != nil || fields == nil {
		return p, errors.New("规则 params 必须为对象")
	}
	allowed := map[string]bool{}
	switch r.Kind {
	case "server.offline", "core.down":
		allowed["minutes"] = true
		if p.Minutes < 1 || p.Minutes > 1440 {
			return p, errors.New("minutes 必须在 1–1440 之间")
		}
	case "server.cpu", "server.mem", "server.disk":
		allowed["percent"] = true
		allowed["minutes"] = true
		if p.Minutes < 1 || p.Minutes > 15 || p.Percent <= 0 || p.Percent > 100 {
			return p, errors.New("资源规则 minutes 必须为 1–15，percent 必须大于 0 且不超过 100")
		}
	case "ping.loss":
		allowed["percent"] = true
		if p.Percent <= 0 || p.Percent > 100 {
			return p, errors.New("percent 必须大于 0 且不超过 100")
		}
	case "server.traffic":
		allowed["percents"] = true
		if err := thresholds(p.Percents, []int{80, 90, 100}); err != nil {
			return p, err
		}
	case "subscriber.quota":
		allowed["percents"] = true
		if err := thresholds(p.Percents, []int{80, 100}); err != nil {
			return p, err
		}
	case "server.expire":
		allowed["days"] = true
		if err := thresholds(p.Days, []int{7, 3, 1}); err != nil {
			return p, err
		}
	}
	for k := range fields {
		if !allowed[k] {
			return p, fmt.Errorf("规则 %s 不支持参数 %s", r.Kind, k)
		}
	}
	return p, nil
}
func thresholds(values, allowed []int) error {
	if len(values) == 0 || len(values) > len(allowed) {
		return fmt.Errorf("阈值须从 %v 选择，至少一项", allowed)
	}
	seen := map[int]bool{}
	for _, v := range values {
		valid := false
		for _, a := range allowed {
			if v == a {
				valid = true
			}
		}
		if !valid || seen[v] {
			return fmt.Errorf("阈值须从 %v 选择且不得重复", allowed)
		}
		seen[v] = true
	}
	return nil
}
func ValidateRules(rules []store.AlertRule) error {
	if len(rules) != len(Kinds) {
		return errors.New("必须一次提交全部 11 条规则")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.Kind] {
			return errors.New("规则 kind 重复")
		}
		seen[r.Kind] = true
		if _, err := ParseParams(r); err != nil {
			return fmt.Errorf("%s: %w", r.Kind, err)
		}
	}
	return nil
}
