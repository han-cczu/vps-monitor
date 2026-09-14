package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
	"vpsmon/server/internal/store"
)

func (d *Deps) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, size := 1, 25
	var err error
	if s := q.Get("page"); s != "" {
		page, err = strconv.Atoi(s)
		if err != nil || page < 1 || page > 1000000 {
			writeError(w, 400, "页码无效")
			return
		}
	}
	if s := q.Get("size"); s != "" {
		size, err = strconv.Atoi(s)
		if err != nil || size < 1 || size > 100 {
			writeError(w, 400, "每页数量须为1–100")
			return
		}
	}
	f := store.AuditFilter{Actor: q.Get("actor"), Action: q.Get("action"), TargetType: q.Get("target_type"), Limit: size, Offset: (page - 1) * size}
	if len(f.Actor) > 200 || len(f.Action) > 200 || len(f.TargetType) > 100 {
		writeError(w, 400, "筛选条件过长")
		return
	}
	if f.From, err = auditTime(q.Get("from")); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if f.To, err = auditTime(q.Get("to")); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if f.From > 0 && f.To > 0 && f.From > f.To {
		writeError(w, 400, "开始时间不能晚于结束时间")
		return
	}
	entries, total, err := d.DB.QueryAudit(r.Context(), f)
	if err != nil {
		writeError(w, 500, "读取审计记录失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"items": entries, "total": total, "page": page, "size": size})
}
func auditTime(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return n, nil
	}
	if v, err := time.Parse(time.RFC3339, s); err == nil {
		return v.Unix(), nil
	}
	return 0, fmt.Errorf("时间须为Unix秒或RFC3339格式")
}
