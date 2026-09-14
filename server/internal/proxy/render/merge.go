package render

import (
	"encoding/json"
	"fmt"
	"slices"
	"vpsmon/server/internal/proxy/model"
)

func merge(cfg map[string]any, raw json.RawMessage) ([]string, error) {
	warnings := []string{}
	if len(raw) == 0 {
		return warnings, nil
	}
	if _, err := model.Object(raw, 256<<10); err != nil {
		return nil, err
	}
	var extra map[string]any
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, err
	}
	for key, value := range extra {
		switch key {
		case "inbounds", "experimental", "log":
			return nil, fmt.Errorf("高级 JSON 不能覆盖受管字段 %s", key)
		case "outbounds":
			items, ok := value.([]any)
			if !ok || len(items) > 1000 {
				return nil, fmt.Errorf("outbounds 必须是最多 1000 项的数组")
			}
			tags := map[string]bool{"direct": true}
			for _, item := range items {
				b, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("outbound 必须是对象")
				}
				tag, _ := b["tag"].(string)
				kind, _ := b["type"].(string)
				if tag == "" || kind == "" || tags[tag] {
					return nil, fmt.Errorf("outbound tag 重复或缺少 type/tag")
				}
				tags[tag] = true
			}
			cfg[key] = append(cfg[key].([]any), items...)
		case "route":
			route, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("route 必须是对象")
			}
			dst := cfg[key].(map[string]any)
			for k, v := range route {
				if k == "rules" {
					items, ok := v.([]any)
					if !ok {
						return nil, fmt.Errorf("route.rules 必须是数组")
					}
					for _, item := range items {
						if _, ok := item.(map[string]any); !ok {
							return nil, fmt.Errorf("route rule 必须是对象")
						}
					}
					dst[k] = append(dst[k].([]any), items...)
				} else {
					if k == "final" {
						if s, ok := v.(string); !ok || s == "" {
							return nil, fmt.Errorf("route.final 必须是非空字符串")
						}
					}
					dst[k] = v
				}
			}
		default:
			cfg[key] = value
			warnings = append(warnings, "高级 JSON 设置顶层字段 "+key)
		}
	}
	slices.Sort(warnings)
	return warnings, nil
}
