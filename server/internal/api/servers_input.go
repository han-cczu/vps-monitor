package api

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
)

// 节点字段的长度与取值上限。前端表单用同一套规则，这里是最终防线。
const (
	maxServerNameLen      = 64
	maxGroupNameLen       = 32
	maxTagCount           = 10
	maxTagLen             = 16
	maxBandwidthLabelLen  = 32
	maxServerNoteLen      = 500
	maxPublicHostLen      = 253
	maxSortOrder          = 1_000_000
	maxServerPrice        = 1e9
	maxTrafficLimitBytes  = 1 << 60 // 1 EiB，够到天荒地老，又不会让后续换算溢出
	expireAtLayout        = "2006-01-02"
	defaultCurrency       = "CNY"
	defaultBillingCycle   = "month"
	defaultTrafficMode    = "max"
	defaultTrafficResetDy = 1
)

var (
	billingCycles = []string{"month", "quarter", "year", "once"}
	trafficModes  = []string{"out", "in", "sum", "max"}
)

// serverRequest 是创建 / 更新节点的请求体。PUT 是全量覆盖：没传的字段按缺省值处理。
type serverRequest struct {
	Name                  string   `json:"name"`
	Region                string   `json:"region"`
	GroupName             string   `json:"group_name"`
	Tags                  []string `json:"tags"`
	SortOrder             int64    `json:"sort_order"`
	PublicHost            string   `json:"public_host"`
	Price                 float64  `json:"price"`
	Currency              string   `json:"currency"`
	BillingCycle          string   `json:"billing_cycle"`
	ExpireAt              *string  `json:"expire_at"`
	AutoRenew             bool     `json:"auto_renew"`
	TrafficLimit          int64    `json:"traffic_limit"`
	TrafficResetDay       int      `json:"traffic_reset_day"`
	TrafficResetMode      string   `json:"traffic_reset_mode"`
	TrafficPeriodStart    *string  `json:"traffic_period_start"`
	TrafficNextReset      *string  `json:"traffic_next_reset"`
	TrafficExpectedStart  *int64   `json:"traffic_expected_start"`
	TrafficPeriodRevision *int64   `json:"traffic_period_revision"`
	TrafficMode           string   `json:"traffic_mode"`
	BandwidthLabel        string   `json:"bandwidth_label"`
	Note                  string   `json:"note"`
}

// toInput 校验并规范化请求体。返回的 error 是给用户看的中文提示，直接进 400 响应。
func (req serverRequest) toInput() (store.ServerInput, error) {
	var in store.ServerInput

	name := strings.TrimSpace(req.Name)
	switch {
	case name == "":
		return in, errors.New("请填写节点名称")
	case utf8.RuneCountInString(name) > maxServerNameLen:
		return in, fmt.Errorf("节点名称最多 %d 个字符", maxServerNameLen)
	}

	region, err := normalizeRegion(req.Region)
	if err != nil {
		return in, err
	}

	groupName := strings.TrimSpace(req.GroupName)
	if utf8.RuneCountInString(groupName) > maxGroupNameLen {
		return in, fmt.Errorf("分组名最多 %d 个字符", maxGroupNameLen)
	}

	tags, err := normalizeTags(req.Tags)
	if err != nil {
		return in, err
	}

	if req.SortOrder < -maxSortOrder || req.SortOrder > maxSortOrder {
		return in, fmt.Errorf("排序值需要在 ±%d 之间", maxSortOrder)
	}

	publicHost := strings.TrimSpace(req.PublicHost)
	if publicHost != "" && !validPublicHost(publicHost) {
		return in, errors.New("公网地址需要是域名或 IP，不带协议和端口")
	}

	if math.IsNaN(req.Price) || math.IsInf(req.Price, 0) || req.Price < 0 || req.Price > maxServerPrice {
		return in, errors.New("价格需要是 0 以上的数字")
	}

	currency, err := normalizeCurrency(req.Currency)
	if err != nil {
		return in, err
	}

	billingCycle := strings.TrimSpace(req.BillingCycle)
	if billingCycle == "" {
		billingCycle = defaultBillingCycle
	}
	if !slices.Contains(billingCycles, billingCycle) {
		return in, fmt.Errorf("账单周期只能是 %s 之一", strings.Join(billingCycles, " / "))
	}

	expireAt, err := normalizeExpireAt(req.ExpireAt)
	if err != nil {
		return in, err
	}

	if req.TrafficLimit < 0 || req.TrafficLimit > maxTrafficLimitBytes {
		return in, errors.New("流量上限需要是 0 以上的字节数，0 表示不限")
	}

	resetDay := req.TrafficResetDay
	if resetDay == 0 {
		resetDay = defaultTrafficResetDy
	}
	if resetDay < 1 || resetDay > 31 {
		return in, errors.New("流量重置日需要在 1–31 之间")
	}
	resetMode := req.TrafficResetMode
	if resetMode == "" {
		resetMode = "days"
		// Older clients explicitly supplying a reset day keep their monthly rule.
		if req.TrafficResetDay != 0 {
			resetMode = "monthly"
		}
	}
	if resetMode != "monthly" && resetMode != "days" {
		return in, errors.New("流量重置周期只能是每 30 天或每月指定日期")
	}

	trafficMode := strings.TrimSpace(req.TrafficMode)
	if trafficMode == "" {
		trafficMode = defaultTrafficMode
	}
	if !slices.Contains(trafficModes, trafficMode) {
		return in, fmt.Errorf("流量统计模式只能是 %s 之一", strings.Join(trafficModes, " / "))
	}

	bandwidthLabel := strings.TrimSpace(req.BandwidthLabel)
	if utf8.RuneCountInString(bandwidthLabel) > maxBandwidthLabelLen {
		return in, fmt.Errorf("带宽标签最多 %d 个字符", maxBandwidthLabelLen)
	}

	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxServerNoteLen {
		return in, fmt.Errorf("备注最多 %d 个字符", maxServerNoteLen)
	}

	return store.ServerInput{
		Name:             name,
		Region:           region,
		GroupName:        groupName,
		Tags:             tags,
		SortOrder:        req.SortOrder,
		PublicHost:       publicHost,
		Price:            req.Price,
		Currency:         currency,
		BillingCycle:     billingCycle,
		ExpireAt:         expireAt,
		AutoRenew:        req.AutoRenew,
		TrafficLimit:     req.TrafficLimit,
		TrafficResetDay:  resetDay,
		TrafficResetMode: resetMode,
		TrafficMode:      trafficMode,
		BandwidthLabel:   bandwidthLabel,
		Note:             note,
	}, nil
}

func (req serverRequest) scheduleInput(in store.ServerInput, now time.Time, creating bool) (*traffic.ScheduleInput, error) {
	if !creating && req.TrafficPeriodStart == nil && req.TrafficNextReset == nil {
		return nil, nil
	}
	p := traffic.NewPeriod(now, in.TrafficResetMode, in.TrafficResetDay)
	parse := func(raw *string, label string) (time.Time, error) {
		if raw == nil {
			return time.Time{}, fmt.Errorf("请填写%s", label)
		}
		d, err := time.ParseInLocation(time.DateOnly, *raw, now.Location())
		if err != nil {
			return time.Time{}, fmt.Errorf("%s需要是 YYYY-MM-DD 格式的有效日期", label)
		}
		return d, nil
	}
	if req.TrafficPeriodStart != nil || !creating {
		start, err := parse(req.TrafficPeriodStart, "本期流量开始日期")
		if err != nil {
			return nil, err
		}
		p.Start = start.Unix()
		p.NextReset = traffic.UpcomingResetDate(start, now, in.TrafficResetMode, in.TrafficResetDay).Unix()
	}
	if req.TrafficNextReset != nil {
		next, err := parse(req.TrafficNextReset, "下次重置日期")
		if err != nil {
			return nil, err
		}
		p.NextReset = next.Unix()
	}
	if err := traffic.ValidatePeriod(p.Start, p.NextReset, now); err != nil {
		return nil, err
	}
	input := &traffic.ScheduleInput{Start: p.Start, NextReset: p.NextReset}
	if !creating {
		if req.TrafficExpectedStart == nil || req.TrafficPeriodRevision == nil || *req.TrafficExpectedStart <= 0 || *req.TrafficPeriodRevision < 0 {
			return nil, errors.New("请刷新节点信息后再修改流量周期")
		}
		input.ExpectedStart, input.Revision = *req.TrafficExpectedStart, *req.TrafficPeriodRevision
	}
	return input, nil
}

// normalizeRegion 接受空串或 ISO 3166-1 alpha-2，小写自动转大写。
func normalizeRegion(raw string) (string, error) {
	region := strings.ToUpper(strings.TrimSpace(raw))
	if region == "" {
		return "", nil
	}
	if len(region) != 2 || !isASCIIUpperAlpha(region) {
		return "", errors.New("地区需要是两位国家码，如 HK、JP、US")
	}
	return region, nil
}

// normalizeCurrency 接受空串（按 CNY）或三位 ISO 4217 代码。
func normalizeCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" {
		return defaultCurrency, nil
	}
	if len(currency) != 3 || !isASCIIUpperAlpha(currency) {
		return "", errors.New("货币需要是三位代码，如 CNY、USD、HKD")
	}
	return currency, nil
}

// normalizeTags 去空白、丢空串、按原顺序去重，并检查数量与长度。
func normalizeTags(raw []string) ([]string, error) {
	tags := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagLen {
			return nil, fmt.Errorf("标签「%s」超过 %d 个字符", t, maxTagLen)
		}
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	if len(tags) > maxTagCount {
		return nil, fmt.Errorf("标签最多 %d 个", maxTagCount)
	}
	return tags, nil
}

// normalizeExpireAt 接受 null、空串（都表示不设到期）或 YYYY-MM-DD。
func normalizeExpireAt(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(expireAtLayout, s)
	if err != nil {
		return nil, errors.New("到期日格式需要是 YYYY-MM-DD")
	}
	// time.Parse 接受 2026-1-2 这种写法，回写成标准格式免得库里两种形式并存
	normalized := t.Format(expireAtLayout)
	return &normalized, nil
}

// validPublicHost 校验域名或 IP（不含协议、端口、路径）。
func validPublicHost(host string) bool {
	if len(host) > maxPublicHostLen {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	// 域名：允许末尾的根点，每段 1–63 字符，只含字母数字和连字符，不以连字符开头或结尾
	name := strings.TrimSuffix(host, ".")
	if name == "" {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

func isASCIIUpperAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}
