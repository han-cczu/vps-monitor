package metrics

import (
	"context"
	"log/slog"
	"time"

	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// 保留期的 settings key 与默认值。
const (
	SettingMinuteDays = "retention.metrics_minute_days"
	SettingHourDays   = "retention.metrics_hour_days"

	DefaultMinuteDays = 7
	DefaultHourDays   = 365
)

// rollupOffset 是每小时内跑降采样的时刻：第 5 分钟。
//
// 不在整点就跑，是给「刚过整点才落库的那一分钟」留时间——
// 聚合器每分钟第 2 秒才把上一分钟写进库。
const rollupOffset = 5 * time.Minute

// catchUpLimit 是启动补跑最多往回补多少小时。
//
// 面板停了三个月再开，没必要把三个月的分钟行全扫一遍——分钟表最多也只留 7 天，
// 再往前的分钟行早就被清理了，补也补不出东西来。
const catchUpLimit = 24 * 8

// hourStore 是 Rollup 需要的库能力。
type hourStore interface {
	RollupHours(ctx context.Context, from, to int64) (int64, error)
	LatestHourTS(ctx context.Context) (int64, error)
	EarliestMinuteTS(ctx context.Context) (int64, error)
	DeleteMetricsBefore(ctx context.Context, table string, cutoff int64) (int64, error)
	GetSetting(ctx context.Context, key string, v any) (bool, error)
}

// Rollup 负责降采样与过期清理。
type Rollup struct {
	db  hourStore
	now func() time.Time
}

// NewRollup 新建降采样任务。
func NewRollup(db hourStore) *Rollup {
	return &Rollup{db: db, now: time.Now}
}

// Run 启动时先补跑一次，之后每小时第 5 分钟跑一次，直到 ctx 结束。
func (r *Rollup) Run(ctx context.Context) {
	if err := r.CatchUp(ctx); err != nil {
		slog.Error("启动补跑降采样失败", "err", err)
	}
	if err := r.Cleanup(ctx); err != nil {
		slog.Error("启动清理过期指标失败", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(untilNextRollup(r.now())):
			if err := r.RollupPreviousHour(ctx); err != nil {
				slog.Error("降采样失败", "err", err)
			}
			if err := r.Cleanup(ctx); err != nil {
				slog.Error("清理过期指标失败", "err", err)
			}
		}
	}
}

// RollupPreviousHour 把上一个完整小时的分钟行降采样成一行小时行。
func (r *Rollup) RollupPreviousHour(ctx context.Context) error {
	to := r.now().Unix() / 3600 * 3600
	from := to - 3600

	n, err := r.db.RollupHours(ctx, from, to)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("降采样完成", "hour", time.Unix(from, 0).Format(time.RFC3339), "rows", n)
	}
	return nil
}

// CatchUp 补齐停机期间漏掉的小时行。
//
// 从「已有的最后一个小时行之后」开始，一直补到当前这个小时之前；
// 小时表为空时从分钟表最早的一行算起。一次 SQL 按小时分组全部写完，
// 不用一小时一小时地循环。
func (r *Rollup) CatchUp(ctx context.Context) error {
	to := r.now().Unix() / 3600 * 3600

	latest, err := r.db.LatestHourTS(ctx)
	if err != nil {
		return err
	}

	var from int64
	switch {
	case latest > 0:
		from = latest + 3600
	default:
		earliest, err := r.db.EarliestMinuteTS(ctx)
		if err != nil {
			return err
		}
		if earliest == 0 {
			return nil // 分钟表也是空的，没什么可补
		}
		from = earliest / 3600 * 3600
	}

	if from >= to {
		return nil
	}
	if limit := to - catchUpLimit*3600; from < limit {
		slog.Warn("补跑区间过长，只补最近的部分",
			"skipped_hours", (limit-from)/3600, "limit_hours", catchUpLimit)
		from = limit
	}

	n, err := r.db.RollupHours(ctx, from, to)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("补跑降采样完成",
			"from", time.Unix(from, 0).Format(time.RFC3339),
			"to", time.Unix(to, 0).Format(time.RFC3339),
			"rows", n)
	}
	return nil
}

// Cleanup 按保留期删掉过期的分钟行与小时行。
func (r *Rollup) Cleanup(ctx context.Context) error {
	now := r.now().Unix()

	minuteDays := r.retentionDays(ctx, SettingMinuteDays, DefaultMinuteDays)
	hourDays := r.retentionDays(ctx, SettingHourDays, DefaultHourDays)

	for _, item := range []struct {
		table string
		days  int
	}{
		{store.TableMetricsMinute, minuteDays},
		{store.TableMetricsHour, hourDays},
	} {
		cutoff := now - int64(item.days)*86400
		n, err := r.db.DeleteMetricsBefore(ctx, item.table, cutoff)
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Info("清理过期指标", "table", item.table, "days", item.days, "rows", n)
		}
	}
	return nil
}

// retentionDays 读保留天数，读不到或值不合理就用默认值。
//
// 保留期配错（比如填 0）会把历史一次性删光，所以这里宁可忽略非法值也不照做。
func (r *Rollup) retentionDays(ctx context.Context, key string, def int) int {
	var days int
	found, err := r.db.GetSetting(ctx, key, &days)
	if err != nil {
		slog.Warn("读取保留期设置失败，用默认值", "key", key, "default", def, "err", err)
		return def
	}
	if !found {
		return def
	}
	if days < 1 {
		slog.Warn("保留期设置不合理，用默认值", "key", key, "value", days, "default", def)
		return def
	}
	return days
}

// untilNextRollup 返回距下一个「整点 + rollupOffset」还有多久。理由同 untilNextFlush。
func untilNextRollup(now time.Time) time.Duration {
	next := now.Truncate(time.Hour).Add(rollupOffset)
	if !next.After(now) {
		next = next.Add(time.Hour)
	}
	return next.Sub(now)
}
