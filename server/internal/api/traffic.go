package api

import (
	"net/http"
	"strconv"

	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
)

func (d *Deps) trafficHistory(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}
	months := 12
	if raw := r.URL.Query().Get("months"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 120 {
			writeError(w, 400, "months 必须是 1–120 的整数")
			return
		}
		months = n
	}
	if d.Traffic != nil {
		if err := d.Traffic.Flush(r.Context()); err != nil {
			serverError(w, "flush traffic", err)
			return
		}
	}
	periods, err := d.DB.TrafficHistory(r.Context(), s.ID, months)
	if err != nil {
		serverError(w, "traffic history", err)
		return
	}
	type row struct {
		store.TrafficPeriod
		Used int64 `json:"used"`
	}
	out := make([]row, 0, len(periods))
	for _, p := range periods {
		out = append(out, row{p, traffic.Used(p, s.TrafficMode)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Deps) withTraffic(dto serverDTO) serverDTO {
	if d.Traffic != nil {
		if t := d.Traffic.SnapshotFor(dto.ID); t != nil {
			dto.TrafficUsed = t.Used
		}
	}
	return dto
}
