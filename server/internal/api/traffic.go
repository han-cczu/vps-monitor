package api

import (
	"errors"
	"net/http"
	"strconv"

	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
)

func (d *Deps) trafficCalibration(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}
	if d.Traffic == nil {
		writeError(w, http.StatusServiceUnavailable, "流量统计尚未就绪")
		return
	}
	result, err := d.Traffic.Calibration(r.Context(), s.ID)
	if err != nil {
		d.trafficCalibrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Deps) calibrateTraffic(w http.ResponseWriter, r *http.Request) {
	s, ok := d.lookupServer(w, r)
	if !ok {
		return
	}
	if d.Traffic == nil {
		writeError(w, http.StatusServiceUnavailable, "流量统计尚未就绪")
		return
	}
	// Pointers distinguish an explicit zero from an omitted/null direction.
	var req struct {
		PeriodStart *int64 `json:"period_start"`
		Revision    *int64 `json:"calibration_revision"`
		In          *int64 `json:"in"`
		Out         *int64 `json:"out"`
		Mode        string `json:"mode"`
		ResetDay    int    `json:"reset_day"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PeriodStart == nil || req.Revision == nil || req.In == nil || req.Out == nil || *req.PeriodStart <= 0 || *req.Revision < 0 || req.ResetDay < 1 || req.ResetDay > 31 || (req.Mode != "in" && req.Mode != "out" && req.Mode != "sum" && req.Mode != "max") {
		writeError(w, http.StatusBadRequest, "请完整填写本期入站、出站用量和当前账期信息")
		return
	}
	result, err := d.Traffic.Calibrate(r.Context(), s.ID, traffic.CalibrationInput{PeriodStart: *req.PeriodStart, Revision: *req.Revision, In: *req.In, Out: *req.Out, Mode: req.Mode, ResetDay: req.ResetDay})
	if err != nil {
		d.trafficCalibrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Deps) trafficCalibrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFoundServer(w)
	case errors.Is(err, traffic.ErrCalibrationInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, traffic.ErrCalibrationChanged), errors.Is(err, traffic.ErrCalibrationStale):
		writeError(w, http.StatusConflict, err.Error())
	default:
		serverError(w, "traffic calibration", err)
	}
}

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
