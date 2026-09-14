package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"vpsmon/server/internal/store"
)

type AgentStore interface {
	FindServerByTokenHash(context.Context, string) (*store.Server, error)
}
type agentContextKey struct{}

// AgentMiddleware 用于 Agent WebSocket 和核心文件下载，共用节点 token 校验。
func AgentMiddleware(db AgentStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := BearerToken(r.Header.Get("Authorization"))
			if token != "" {
				s, err := db.FindServerByTokenHash(r.Context(), HashAgentToken(token))
				if err == nil {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentContextKey{}, s)))
					return
				}
				if !errors.Is(err, store.ErrNotFound) {
					slog.Error("agent 鉴权查库失败", "err", err)
				}
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unauthorized"})
		})
	}
}

func AgentFromContext(ctx context.Context) *store.Server {
	s, _ := ctx.Value(agentContextKey{}).(*store.Server)
	return s
}
