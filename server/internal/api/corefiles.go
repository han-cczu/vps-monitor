package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/corefiles"
)

func (d *Deps) coreReady(w http.ResponseWriter) bool {
	if d.CoreFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "核心文件服务未配置")
		return false
	}
	return true
}
func coreError(w http.ResponseWriter, err error) {
	var bodyLimit *http.MaxBytesError
	switch {
	case errors.Is(err, corefiles.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, corefiles.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, corefiles.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, corefiles.ErrTooLarge), errors.As(err, &bodyLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "文件不能超过 64 MiB")
	case errors.Is(err, corefiles.ErrFetch):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		serverError(w, "core files", err)
	}
}
func (d *Deps) listCoreFiles(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	versions, e := d.CoreFiles.List(r.Context())
	if e != nil {
		coreError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "pinned_version": corefiles.PinnedVersion})
}
func (d *Deps) coreTransfer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case d.coreSlots <- struct{}{}:
			defer func() { <-d.coreSlots }()
		default:
			writeError(w, http.StatusServiceUnavailable, "已有核心文件正在传输，请稍后重试")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		// Bound slow upload reads too; a context alone does not interrupt a stalled request body.
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(2 * time.Minute))
		defer controller.SetReadDeadline(time.Time{})
		next(w, r.WithContext(ctx))
	}
}
func (d *Deps) uploadCoreFile(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, corefiles.MaxFileSize+(64<<10))
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		var limit *http.MaxBytesError
		if errors.As(e, &limit) {
			coreError(w, e)
		} else {
			writeError(w, http.StatusBadRequest, "需要 multipart 文件及 version、arch、sha256 字段")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 || len(r.MultipartForm.Value) != 3 {
		writeError(w, http.StatusBadRequest, "每次仅上传一个 file，并提供 version、arch、sha256")
		return
	}
	for _, key := range []string{"version", "arch", "sha256"} {
		if len(r.MultipartForm.Value[key]) != 1 {
			writeError(w, http.StatusBadRequest, "缺少或重复字段："+key)
			return
		}
	}
	v, a, sum := r.MultipartForm.Value["version"][0], r.MultipartForm.Value["arch"][0], r.MultipartForm.Value["sha256"][0]
	file, e := r.MultipartForm.File["file"][0].Open()
	if e != nil {
		coreError(w, e)
		return
	}
	defer file.Close()
	artifact, e := d.CoreFiles.Put(r.Context(), v, a, file, sum)
	if e != nil {
		coreError(w, e)
		return
	}
	audit.Record(r.Context(), d.DB, "corefile.upload", "corefile", v+"/"+a, nil, artifact)
	writeJSON(w, http.StatusCreated, map[string]any{"version": v, "file": artifact})
}
func (d *Deps) fetchCoreFile(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	var in struct {
		Version string `json:"version"`
		Arch    string `json:"arch"`
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	artifact, e := d.CoreFiles.Fetch(r.Context(), corefiles.FetchClient(), in.Version, in.Arch, in.URL, in.SHA256)
	if e != nil {
		coreError(w, e)
		return
	}
	audit.Record(r.Context(), d.DB, "corefile.fetch", "corefile", in.Version+"/"+in.Arch, nil, artifact)
	writeJSON(w, http.StatusCreated, map[string]any{"version": in.Version, "file": artifact})
}
func (d *Deps) setCurrentCore(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if e := d.CoreFiles.SetCurrent(r.Context(), in.Version); e != nil {
		coreError(w, e)
		return
	}
	audit.Record(r.Context(), d.DB, "corefile.set_current", "corefile", in.Version, nil, map[string]string{"version": in.Version})
	writeJSON(w, http.StatusOK, map[string]string{"version": in.Version})
}
func (d *Deps) deleteCoreVersion(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	v := chi.URLParam(r, "version")
	if e := d.CoreFiles.Delete(r.Context(), v); e != nil {
		coreError(w, e)
		return
	}
	audit.Record(r.Context(), d.DB, "corefile.delete", "corefile", v, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
func (d *Deps) downloadCoreFile(w http.ResponseWriter, r *http.Request) {
	if !d.coreReady(w) {
		return
	}
	v, a := chi.URLParam(r, "version"), chi.URLParam(r, "arch")
	file, meta, e := d.CoreFiles.Open(v, a)
	if e != nil {
		coreError(w, e)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sing-box-%s-linux-%s"`, v, a))
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	w.Header().Set("X-Checksum-Sha256", meta.SHA256)
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "sing-box-"+v, time.Unix(meta.UploadedAt, 0), file)
}
