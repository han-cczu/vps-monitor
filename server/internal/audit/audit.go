// Package audit 记录审计日志：谁、何时、从哪个 IP、对什么做了什么、改前改后是什么。
//
// 所有写接口都应调用 Record；系统任务用 context.Background()，actor 会记为 "system"。
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

// SystemActor 是没有登录用户（后台任务）时记录的操作者。
const SystemActor = "system"

type ipKey struct{}

// Middleware 把客户端 IP 放进请求上下文，供 Record 使用。
// 要挂在 api 包装配的 ClientIPFrom* 中间件之后。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ipKey{}, ClientIP(r))))
	})
}

// ClientIP 返回请求的客户端 IP。
//
// 优先用 chi 的 ClientIPFrom* 中间件按可信代理配置解析出的地址；
// 没解析出来（没配代理、或代理头不可信被 fail-closed 丢弃）就退回 TCP 连接地址。
// 无论如何都不直接采信请求头——那是可以伪造的，会把登录限速和审计一起骗过去。
func ClientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IPFromContext 取出 Middleware 放进去的客户端 IP，没有则返回空串。
func IPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(ipKey{}).(string)
	return ip
}

// Record 写一条审计记录。actor 取 ctx 里的登录用户，没有则为 system；
// before / after 会 JSON 序列化，传 nil 则留空。审计写失败只记日志，不影响主流程。
func Record(ctx context.Context, db *store.DB, action, targetType, targetID string, before, after any) {
	entry := Entry(ctx, action, targetType, targetID, before, after)
	// 请求被客户端中断时也要把审计写完，所以去掉取消信号、只保留 ctx 里的值。
	if _, err := db.InsertAudit(context.WithoutCancel(ctx), entry); err != nil {
		slog.Error("audit record failed", "action", action, "target", targetType+"/"+targetID, "err", err)
	}
}

// Entry builds an audit record for operations that persist their audit atomically.
func Entry(ctx context.Context, action, targetType, targetID string, before, after any) store.AuditEntry {
	actor := SystemActor
	if p, ok := auth.PrincipalFromContext(ctx); ok {
		actor = p.Name
	}

	return store.AuditEntry{
		TS:         time.Now().Unix(),
		Actor:      actor,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Before:     toJSON(before),
		After:      toJSON(after),
		IP:         IPFromContext(ctx),
	}
}

func toJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
