package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

const (
	minPasswordLen = 10
	maxPasswordLen = 256
)

// userDTO 的形状对齐前端 starter 的 user 对象（src/auth/types.ts）。
type userDTO struct {
	ID          int64   `json:"id"`
	DisplayName string  `json:"displayName"`
	Email       string  `json:"email"`
	PhotoURL    *string `json:"photoURL"`
	Role        string  `json:"role"`
}

func toUserDTO(u *store.User) userDTO {
	return userDTO{ID: u.ID, DisplayName: u.Username, Email: u.Username, Role: auth.RoleAdmin}
}

// acquireVerifySlot 取一个 argon2 校验槽位。取不到（排队超时或请求已断）返回 false。
func (d *Deps) acquireVerifySlot(ctx context.Context) bool {
	timer := time.NewTimer(verifyQueueTimeout)
	defer timer.Stop()

	select {
	case d.verifySem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (d *Deps) releaseVerifySlot() { <-d.verifySem }

func busy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusServiceUnavailable, "服务器繁忙，请稍后再试")
}

type signInRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"` // starter 的字段名，按用户名处理
	Password string `json:"password"`
}

type signInResponse struct {
	AccessToken string  `json:"accessToken"`
	ExpiresAt   int64   `json:"expiresAt"` // Unix 秒
	User        userDTO `json:"user"`
}

// signIn 处理 POST /api/auth/sign-in。
func (d *Deps) signIn(w http.ResponseWriter, r *http.Request) {
	ip := audit.ClientIP(r)

	if locked, retryAfter := d.Limiter.Locked(ip); locked {
		tooManyAttempts(w, retryAfter.Seconds())
		return
	}

	var req signInRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.TrimSpace(req.Email)
	}
	if username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "请输入用户名和密码")
		return
	}

	// argon2 很贵，先排队拿槽位
	if !d.acquireVerifySlot(r.Context()) {
		busy(w)
		return
	}
	defer d.releaseVerifySlot()

	// 排队期间可能已经被别的请求打到锁定线，再查一次，把并发窗口收敛到槽位数
	if locked, retryAfter := d.Limiter.Locked(ip); locked {
		tooManyAttempts(w, retryAfter.Seconds())
		return
	}

	user, err := d.DB.GetUserByUsername(r.Context(), username)
	switch {
	case errors.Is(err, store.ErrNotFound):
		auth.VerifyDummy(req.Password)
		d.rejectSignIn(w, ip, username)
		return
	case err != nil:
		slog.Error("sign-in: query user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	ok, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		slog.Error("sign-in: verify password failed", "user_id", user.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if !ok {
		d.rejectSignIn(w, ip, username)
		return
	}

	d.Limiter.Reset(ip)

	principal := auth.Principal{ID: user.ID, Name: user.Username, Role: auth.RoleAdmin}
	token, exp, err := d.Tokens.Issue(principal)
	if err != nil {
		slog.Error("sign-in: issue token failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	ctx := auth.ContextWithPrincipal(r.Context(), principal)
	audit.Record(ctx, d.DB, "auth.sign_in", "user", strconv.FormatInt(user.ID, 10), nil, nil)
	slog.Info("sign-in ok", "username", user.Username, "ip", ip)

	writeJSON(w, http.StatusOK, signInResponse{AccessToken: token, ExpiresAt: exp.Unix(), User: toUserDTO(user)})
}

// rejectSignIn 记一次失败并返回 401。触发锁定的那一次仍返回 401，之后的请求才是 429。
func (d *Deps) rejectSignIn(w http.ResponseWriter, ip, username string) {
	locked, _ := d.Limiter.Fail(ip)
	slog.Warn("sign-in rejected", "username", username, "ip", ip, "locked", locked)
	writeError(w, http.StatusUnauthorized, "用户名或密码错误")
}

func tooManyAttempts(w http.ResponseWriter, retryAfterSeconds float64) {
	secs := int(math.Ceil(retryAfterSeconds))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	minutes := (secs + 59) / 60
	writeError(w, http.StatusTooManyRequests, "尝试次数过多，请 "+strconv.Itoa(minutes)+" 分钟后再试")
}

// me 处理 GET /api/auth/me。
func (d *Deps) me(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	user, err := d.DB.GetUserByID(r.Context(), p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// token 还没过期但用户已经没了
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	case err != nil:
		slog.Error("me: query user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": toUserDTO(user)})
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// changePassword 处理 POST /api/auth/password。成功 204，并写审计 auth.password_change。
func (d *Deps) changePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 旧密码猜测也要限速：token 可能是从别处捡来的，别让它在这里无限试
	limitKey := "pw:" + strconv.FormatInt(p.ID, 10)
	if locked, retryAfter := d.Limiter.Locked(limitKey); locked {
		tooManyAttempts(w, retryAfter.Seconds())
		return
	}

	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch {
	case req.OldPassword == "":
		writeError(w, http.StatusBadRequest, "请输入当前密码")
		return
	case utf8.RuneCountInString(req.NewPassword) < minPasswordLen:
		writeError(w, http.StatusBadRequest, "新密码至少 "+strconv.Itoa(minPasswordLen)+" 位")
		return
	case len(req.NewPassword) > maxPasswordLen:
		writeError(w, http.StatusBadRequest, "新密码过长")
		return
	case req.NewPassword == req.OldPassword:
		writeError(w, http.StatusBadRequest, "新密码不能与当前密码相同")
		return
	}

	if !d.acquireVerifySlot(r.Context()) {
		busy(w)
		return
	}
	defer d.releaseVerifySlot()

	user, err := d.DB.GetUserByID(r.Context(), p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	case err != nil:
		slog.Error("change password: query user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	ok, err = auth.VerifyPassword(user.PasswordHash, req.OldPassword)
	if err != nil {
		slog.Error("change password: verify failed", "user_id", user.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if !ok {
		locked, _ := d.Limiter.Fail(limitKey)
		slog.Warn("change password rejected: wrong old password",
			"username", user.Username, "ip", audit.ClientIP(r), "locked", locked)
		// 用 400 而不是 401：401 会让前端把整个会话清掉
		writeError(w, http.StatusBadRequest, "当前密码错误")
		return
	}
	d.Limiter.Reset(limitKey)

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		slog.Error("change password: hash failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if err := d.DB.UpdateUserPassword(r.Context(), user.ID, hash); err != nil {
		slog.Error("change password: update failed", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}

	// 密码哈希不进审计，只记"改了"
	audit.Record(r.Context(), d.DB, "auth.password_change", "user", strconv.FormatInt(user.ID, 10), nil, nil)
	slog.Info("password changed", "username", user.Username)

	w.WriteHeader(http.StatusNoContent)
}
