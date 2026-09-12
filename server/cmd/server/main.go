// Command server 是面板服务端：汇聚 agent 上报、提供 REST 与 WebSocket、内嵌前端。
//
// 步骤 01 只做最小可运行骨架：健康检查 + 内嵌前端的 SPA 回退。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"vpsmon/server/web"
)

// version 由构建时 -ldflags "-X main.version=$(VERSION)" 注入。
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(env("VM_LOG_LEVEL", "info")),
	})))

	listen := env("VM_LISTEN", ":9000")

	srv := &http.Server{
		Addr:              listen,
		Handler:           newRouter(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("server starting", "listen", listen, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("server stopping")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "err", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}

func newRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/api/health", handleHealth)

	// 其余路径交给内嵌前端，前端路由用 SPA 回退。
	r.NotFound(spaHandler(web.FS()))

	return r
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version})
}

// spaHandler 提供内嵌前端：命中文件就返回文件，没命中就回退到 index.html；
// /api 下没匹配到路由的一律返回 JSON 404，不要回退成 HTML。
func spaHandler(dist fs.FS) http.HandlerFunc {
	files := http.FileServer(http.FS(dist))

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if f, err := dist.Open(name); err == nil {
				_ = f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}

		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write json failed", "err", err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseLevel(s string) slog.Level {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return lv
}
