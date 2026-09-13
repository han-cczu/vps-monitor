package api

import (
	"errors"
	"math"
	"net/http"
	"time"

	"vpsmon/server/internal/store"
)

// ----------------------------------------------------------------------

// historyRange 是一个可选的时间窗：多长、用哪张表、步长多少。
type historyRange struct {
	span  time.Duration
	table string
	step  int // 秒
}

// 支持的 range 取值。1h / 24h 读分钟表，7d / 30d 读小时表——
// 30 天的分钟行有 4 万多条，画成曲线既慢又没有意义。
var historyRanges = map[string]historyRange{
	"1h":  {span: time.Hour, table: store.TableMetricsMinute, step: 60},
	"24h": {span: 24 * time.Hour, table: store.TableMetricsMinute, step: 60},
	"7d":  {span: 7 * 24 * time.Hour, table: store.TableMetricsHour, step: 3600},
	"30d": {span: 30 * 24 * time.Hour, table: store.TableMetricsHour, step: 3600},
}

// historyPoint 是曲线上的一个点。字段名比库里的短，和 snapshot 一个路数：
// 24h 有 1440 个点，字段名占的字节比数值还多。
type historyPoint struct {
	TS int64 `json:"ts"`

	CPU    float64 `json:"cpu"`
	CPUMax float64 `json:"cpu_max"`
	Mem    int64   `json:"mem"`
	Swap   int64   `json:"swap"`
	Disk   int64   `json:"disk"`
	Load1  float64 `json:"load1"`

	Rx    int64 `json:"rx"`
	RxMax int64 `json:"rx_max"`
	Tx    int64 `json:"tx"`
	TxMax int64 `json:"tx_max"`

	TCP   int `json:"tcp"`
	UDP   int `json:"udp"`
	Procs int `json:"procs"`
}

// historyResponse 是 GET /api/servers/{id}/history 的响应。
type historyResponse struct {
	Step   int            `json:"step"` // 秒，60 或 3600
	From   int64          `json:"from"`
	To     int64          `json:"to"`
	Points []historyPoint `json:"points"`

	// 画百分比要用的总量，来自 host info；节点没上报过就是 0。
	MemTotal  int64 `json:"mem_total"`
	DiskTotal int64 `json:"disk_total"`
}

// history 处理 GET /api/servers/{id}/history?range=1h|24h|7d|30d。
//
// 缺失的时间点不补零：节点掉线那几分钟本来就没有数据，补成 0 会在曲线上画出
// 一段「CPU 掉到 0」的假象。前端用 datetime 轴，缺口自然留空。
func (d *Deps) history(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}

	key := r.URL.Query().Get("range")
	if key == "" {
		key = "24h"
	}
	rng, ok := historyRanges[key]
	if !ok {
		writeError(w, http.StatusBadRequest, "range 只能是 1h、24h、7d、30d")
		return
	}

	now := time.Now()
	to := now.Unix()
	from := now.Add(-rng.span).Unix()
	// 对齐到步长边界，前端画出来的第一个点才不会半悬在轴外
	from = from / int64(rng.step) * int64(rng.step)

	rows, err := d.DB.QueryMetrics(r.Context(), rng.table, s.ID, from, to)
	if err != nil {
		serverError(w, "query metrics", err)
		return
	}

	resp := historyResponse{
		Step:   rng.step,
		From:   from,
		To:     to,
		Points: make([]historyPoint, 0, len(rows)),
	}
	for i := range rows {
		resp.Points = append(resp.Points, toHistoryPoint(&rows[i]))
	}

	// 总量给前端画百分比用。读不到（节点从没连过）就留 0，前端按可空处理。
	host, err := d.DB.GetHostInfo(r.Context(), s.ID)
	switch {
	case err == nil:
		resp.MemTotal = host.MemTotal
		resp.DiskTotal = host.DiskTotal
	case !errors.Is(err, store.ErrNotFound):
		serverError(w, "get host info", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func toHistoryPoint(row *store.MetricRow) historyPoint {
	return historyPoint{
		TS:     row.TS,
		CPU:    round2(row.CPUAvg),
		CPUMax: round2(row.CPUMax),
		Mem:    row.MemUsed,
		Swap:   row.SwapUsed,
		Disk:   row.DiskUsed,
		Load1:  round2(row.Load1),
		Rx:     row.RxRateAvg,
		RxMax:  row.RxRateMax,
		Tx:     row.TxRateAvg,
		TxMax:  row.TxRateMax,
		TCP:    row.TCP,
		UDP:    row.UDP,
		Procs:  row.Procs,
	}
}

// round2 把浮点四舍五入到两位小数。
//
// 不截的话 CPU 会序列化成 3.0234000000000002 这种值：1440 个点乘几个字段，
// 白白多出几十 KB，而曲线上两位小数已经看不出差别了。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
