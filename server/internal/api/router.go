// Package api 装配 HTTP 路由：中间件、REST handler、内嵌前端的 SPA 回退。
//
// 约定：
//   - 错误响应统一为 {"message": string}；参数错误 400，未登录 401，登录限速 429
//   - /api/health 与 /api/auth/sign-in 公开，其余 /api/* 都要 JWT
//   - /api/* 没匹配到的路径返回 JSON 404，不回退成 HTML
package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/config"
	"vpsmon/server/internal/store"
)

// maxConcurrentVerify 是同时进行的 argon2 校验数上限。
// argon2id 每次要吃 64 MiB 内存，不设闸的话几百个并发登录请求就能把进程打爆。
const maxConcurrentVerify = 4

// verifyQueueTimeout 是等待校验槽位的上限，超时返回 503 而不是让请求无限堆积。
const verifyQueueTimeout = 5 * time.Second

// Deps 是路由需要的全部依赖，由 main 装配。
type Deps struct {
	DB             *store.DB
	Tokens         *auth.Tokens
	Limiter        *auth.Limiter
	Version        string
	Web            http.Handler // 内嵌前端（server/web.Handler()）
	TrustedProxies config.TrustedProxies

	// verifySem 由 NewRouter 初始化，限制并发密码校验数。
	verifySem chan struct{}
}

// NewRouter 构建根路由。
func NewRouter(deps Deps) http.Handler {
	d := &deps
	d.verifySem = make(chan struct{}, maxConcurrentVerify)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)

	// 客户端 IP：先按 TCP 连接取（不可伪造，兜底），再按可信代理配置用 X-Forwarded-For 覆盖。
	// 不用 chi 的 RealIP——它会无条件采信 True-Client-IP / X-Real-IP，官方已标记弃用。
	r.Use(middleware.ClientIPFromRemoteAddr)
	switch {
	case len(d.TrustedProxies.CIDRs) > 0:
		r.Use(middleware.ClientIPFromXFF(d.TrustedProxies.CIDRs...))
	case d.TrustedProxies.Count > 0:
		r.Use(middleware.ClientIPFromXFFTrustedProxies(d.TrustedProxies.Count))
	}

	r.Use(requestLogger)
	r.Use(recoverer)
	r.Use(middleware.Compress(5))
	r.Use(audit.Middleware)

	r.Route("/api", func(api chi.Router) {
		api.Get("/health", d.health)
		api.Post("/auth/sign-in", d.signIn)

		api.Group(func(protected chi.Router) {
			protected.Use(d.Tokens.Middleware)
			protected.Get("/auth/me", d.me)
			protected.Post("/auth/password", d.changePassword)
		})

		api.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	})

	// 其余路径全部交给内嵌前端（静态文件 + SPA 回退）
	r.NotFound(d.Web.ServeHTTP)

	return r
}

func (d *Deps) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": d.Version})
}

// requestLogger 用 slog 记访问日志。只记路径不记 query：订阅链接的 token 等敏感信息不能进日志。
// /api/* 记 info，静态资源与健康检查记 debug，免得刷屏。
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			level := slog.LevelDebug
			if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
				level = slog.LevelInfo
			}
			slog.Log(r.Context(), level, "http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"dur_ms", time.Since(start).Milliseconds(),
				"ip", audit.ClientIP(r),
				"req_id", middleware.GetReqID(r.Context()),
			)
		}()

		next.ServeHTTP(ww, r)
	})
}

// recoverer 把 panic 变成统一格式的 500 JSON，并把堆栈按 slog JSON 记下来。
// chi 自带的 Recoverer 只写状态码不写 body，堆栈还是带 ANSI 颜色的纯文本，与本项目的日志和错误约定都不一致。
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			// ErrAbortHandler 是 net/http 约定的"静默中断"，原样抛回去
			if rvr == http.ErrAbortHandler { //nolint:errorlint // 哨兵值按 == 比较，net/http 自己也这么判断
				panic(rvr)
			}

			slog.Error("panic recovered",
				"err", fmt.Sprint(rvr),
				"method", r.Method,
				"path", r.URL.Path,
				"req_id", middleware.GetReqID(r.Context()),
				"stack", string(debug.Stack()),
			)

			// WebSocket 升级中的连接已经不归 http 管了
			if r.Header.Get("Connection") == "Upgrade" {
				return
			}
			// 响应已经写出去一部分就不再追加，免得 net/http 报 superfluous WriteHeader
			if ww, ok := w.(middleware.WrapResponseWriter); ok && ww.Status() != 0 {
				return
			}
			writeError(w, http.StatusInternalServerError, "服务器内部错误")
		}()

		next.ServeHTTP(w, r)
	})
}
