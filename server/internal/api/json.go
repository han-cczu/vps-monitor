package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// maxBodyBytes 是 JSON 请求体上限。面板接口都是小 JSON，1 MiB 绰绰有余。
const maxBodyBytes = 1 << 20

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json failed", "err", err)
	}
}

// writeError 输出统一格式的错误：{"message": "..."}。
func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"message": message})
}

// decodeJSON 把请求体解析到 dst。失败时已经写好 400 响应，调用方直接 return 即可。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return false
	}
	return true
}
