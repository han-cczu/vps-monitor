package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"
	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/store"
)

func (d *Deps) mfa(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ipKey := "mfa-ip:" + audit.ClientIP(r)
	if locked, wait := d.Limiter.Locked(ipKey); locked {
		tooManyAttempts(w, wait.Seconds())
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id, hash, err := d.MFA.Consume(req.Ticket)
	if err != nil {
		d.Limiter.Fail(ipKey)
		writeError(w, 401, auth.ErrMFA.Error())
		return
	}
	key := "mfa-user:" + strconv.FormatInt(id, 10)
	if locked, wait := d.Limiter.Locked(key); locked {
		tooManyAttempts(w, wait.Seconds())
		return
	}
	user, err := d.DB.GetUserByID(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, 500, "验证服务暂时不可用")
		return
	}
	if err != nil || !user.TOTPEnabled || user.PasswordHash != hash {
		d.Limiter.Fail(ipKey)
		d.Limiter.Fail(key)
		writeError(w, 401, auth.ErrMFA.Error())
		return
	}
	if err = d.verifyTOTP(r, user, req.Code); err != nil {
		if !errors.Is(err, auth.ErrMFA) && !errors.Is(err, store.ErrTOTPReplay) && !errors.Is(err, store.ErrTOTPState) {
			writeError(w, 500, "验证服务暂时不可用")
			return
		}
		d.Limiter.Fail(ipKey)
		d.Limiter.Fail(key)
		writeError(w, 401, auth.ErrMFA.Error())
		return
	}
	d.Limiter.Reset(ipKey)
	d.Limiter.Reset(key)
	d.completeSignIn(w, r, user)
}
func (d *Deps) verifyTOTP(r *http.Request, u *store.User, code string) error {
	secret, err := d.MFA.Decrypt(u.ID, u.TOTPSecret)
	if err != nil {
		return err
	}
	now := time.Now()
	step, err := auth.MatchTOTP(code, secret, now)
	if err != nil {
		return err
	}
	return d.DB.ConsumeTOTP(r.Context(), u, step, code, now)
}
func (d *Deps) totpStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFromContext(r.Context())
	u, err := d.DB.GetUserByID(r.Context(), p.ID)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]bool{"enabled": u.TOTPEnabled})
}
func (d *Deps) totpSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, _ := auth.PrincipalFromContext(r.Context())
	key := "totp-manage:" + strconv.FormatInt(p.ID, 10)
	if locked, wait := d.Limiter.Locked(key); locked {
		tooManyAttempts(w, wait.Seconds())
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Password) > maxPasswordLen || req.Password == "" {
		writeError(w, 400, "请输入当前密码")
		return
	}
	if !d.acquireVerifySlot(r.Context()) {
		busy(w)
		return
	}
	defer d.releaseVerifySlot()
	u, err := d.DB.GetUserByID(r.Context(), p.ID)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, req.Password)
	if err != nil || !ok {
		d.Limiter.Fail(key)
		writeError(w, 400, "当前密码错误")
		return
	}
	if u.TOTPEnabled {
		writeError(w, 409, "请先禁用已有二步验证")
		return
	}
	secret, url, err := d.MFA.Setup(u.ID, u.Username, u.PasswordHash)
	if err != nil {
		writeError(w, 500, "无法生成二步验证密钥")
		return
	}
	writeJSON(w, 200, map[string]string{"secret": secret, "url": url})
}
func (d *Deps) totpEnable(w http.ResponseWriter, r *http.Request)  { d.totpToggle(w, r, true) }
func (d *Deps) totpDisable(w http.ResponseWriter, r *http.Request) { d.totpToggle(w, r, false) }
func (d *Deps) totpToggle(w http.ResponseWriter, r *http.Request, enabled bool) {
	p, _ := auth.PrincipalFromContext(r.Context())
	key := "totp-manage:" + strconv.FormatInt(p.ID, 10)
	if locked, wait := d.Limiter.Locked(key); locked {
		tooManyAttempts(w, wait.Seconds())
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := d.DB.GetUserByID(r.Context(), p.ID)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return
	}
	if u.TOTPEnabled == enabled {
		writeError(w, 409, "二步验证状态已改变，请刷新")
		return
	}
	now := time.Now()
	var secret string
	if enabled {
		secret, err = d.MFA.Pending(u.ID, u.PasswordHash)
	} else {
		secret, err = d.MFA.Decrypt(u.ID, u.TOTPSecret)
	}
	var step int64
	if err == nil {
		step, err = auth.MatchTOTP(req.Code, secret, now)
	}
	encrypted := ""
	if err == nil && enabled {
		encrypted, err = d.MFA.Encrypt(u.ID, secret)
	}
	if err != nil {
		d.Limiter.Fail(key)
		writeError(w, 400, auth.ErrMFA.Error())
		return
	}
	action := "auth.totp_disable"
	if enabled {
		action = "auth.totp_enable"
	}
	change := &store.TOTPChange{Secret: encrypted, Enabled: enabled, Audit: store.AuditEntry{TS: now.Unix(), Actor: p.Name, Action: action, TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10), IP: audit.ClientIP(r)}}
	if err = d.DB.ApplyTOTP(r.Context(), u, step, req.Code, now, change); err != nil {
		if errors.Is(err, store.ErrTOTPState) || errors.Is(err, store.ErrTOTPReplay) {
			d.Limiter.Fail(key)
			writeError(w, 409, "二步验证状态已变化或验证码已使用，请刷新重试")
		} else {
			writeError(w, 500, "保存二步验证和审计失败")
		}
		return
	}
	d.MFA.Clear(u.ID)
	d.Limiter.Reset(key)
	w.WriteHeader(204)
}
