package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type principalKey struct{}

// ContextWithPrincipal 把当前用户放进 ctx。
func ContextWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext 取出 ctx 里的当前用户；没有登录用户返回 false。
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Middleware 校验 Authorization: Bearer <token>，失败返回 401 {"message":"unauthorized"}，
// 成功则把 Principal 放进请求上下文。
func (t *Tokens) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r.Header.Get("Authorization"))
		if raw == "" {
			unauthorized(w)
			return
		}
		p, err := t.Parse(raw)
		if err != nil {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), p)))
	})
}

// bearerToken 从 "Bearer xxx" 里取出 xxx；scheme 大小写不敏感。
func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "unauthorized"})
}
