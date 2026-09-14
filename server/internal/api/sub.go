package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"vpsmon/server/internal/proxy/sub"
	"vpsmon/server/internal/store"
)

type subscriptionEntry struct {
	body    []byte
	expires time.Time
	epoch   uint64
}
type subscriptionWindow struct {
	start time.Time
	count int
}
type subscriptionCache struct {
	mu      sync.Mutex
	entries map[string]subscriptionEntry
	limits  map[[32]byte]subscriptionWindow
}

func newSubscriptionCache() *subscriptionCache {
	return &subscriptionCache{entries: map[string]subscriptionEntry{}, limits: map[[32]byte]subscriptionWindow{}}
}
func (c *subscriptionCache) allow(token [32]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, v := range c.limits {
		if now.Sub(v.start) >= time.Minute {
			delete(c.limits, key)
		}
	}
	v := c.limits[token]
	if v.start.IsZero() {
		v.start = now
	}
	if v.count >= 30 {
		return false
	}
	// Unknown tokens never enter this map. Bound memory for large legitimate installations too.
	if len(c.limits) >= 10000 && v.count == 0 {
		return false
	}
	v.count++
	c.limits[token] = v
	return true
}
func (d *Deps) subscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token := chi.URLParam(r, "token")
	if token == "" || len(token) > 256 {
		w.WriteHeader(404)
		return
	}
	epoch := d.DB.SubscriptionEpoch()
	subscriber, err := d.DB.Proxy().SubscriberByToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(404)
		return
	}
	if err != nil {
		slog.Error("subscription lookup failed")
		w.WriteHeader(500)
		return
	}
	// Public requests never log the token, URL, rendered body or credentials.
	defer slog.Info("subscription access", "subscriber_id", subscriber.ID)
	hash := sha256.Sum256([]byte(token))
	now := time.Now()
	if !d.subscriptions.allow(hash, now) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "clash"
	}
	if format != "clash" && format != "clash-provider" {
		writeError(w, 400, "不支持的订阅格式")
		return
	}
	info := fmt.Sprintf("upload=0; download=%d", subscriber.TrafficUsed)
	if subscriber.TrafficLimit > 0 {
		info += fmt.Sprintf("; total=%d", subscriber.TrafficLimit)
	}
	if subscriber.ExpireAt != nil {
		if expiry, parseErr := time.ParseInLocation(time.DateOnly, *subscriber.ExpireAt, time.Local); parseErr == nil {
			info += fmt.Sprintf("; expire=%d", expiry.AddDate(0, 0, 1).Unix())
		}
	}
	w.Header().Set("subscription-userinfo", info)
	w.Header().Set("profile-update-interval", "24")
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(subscriber.Name)+".yaml")
	key := fmt.Sprintf("%x:%s", hash, format)
	c := d.subscriptions
	c.mu.Lock()
	entry, cached := c.entries[key]
	c.mu.Unlock()
	if cached && entry.epoch == epoch && now.Before(entry.expires) {
		_, _ = w.Write(entry.body)
		return
	}
	proxies, err := sub.Collect(r.Context(), d.DB, *subscriber)
	var body []byte
	if err == nil {
		if format == "clash-provider" {
			body, err = sub.RenderClashProvider(proxies)
		} else {
			template := sub.DefaultClashTemplate
			_, err = d.DB.GetSetting(r.Context(), "sub.clash_template", &template)
			if err == nil {
				body, err = sub.RenderClash(proxies, template)
			}
		}
	}
	if err != nil {
		slog.Error("subscription rendering failed: check inbound data and sub.clash_template", "subscriber_id", subscriber.ID, "format", format)
		w.WriteHeader(500)
		return
	}
	if reason := sub.DisabledReason(*subscriber); reason != "" {
		body = append([]byte("# 已停用："+reason+"\n"), body...)
	}
	c.mu.Lock()
	for k, v := range c.entries {
		if v.epoch != epoch || now.After(v.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) < 10000 && d.DB.SubscriptionEpoch() == epoch {
		c.entries[key] = subscriptionEntry{body: body, expires: now.Add(10 * time.Second), epoch: epoch}
	}
	c.mu.Unlock()
	_, _ = w.Write(body)
}
func safeRequestPath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/sub/") {
		return "/sub/[redacted]"
	}
	return r.URL.Path
}
func (d *Deps) subscriberTraffic(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	traffic, err := d.DB.SubscriberTraffic(r.Context(), id, time.Now())
	if proxyError(w, err) {
		return
	}
	writeJSON(w, 200, traffic)
}
