package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/store"
)

type ChannelConfig struct {
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
	URL      string `json:"url,omitempty"`
	Secret   string `json:"secret,omitempty"`
}
type Sender struct {
	Client       *http.Client
	TelegramBase string
}

func NewSender() (*Sender, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if raw := os.Getenv("VM_HTTP_PROXY"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("VM_HTTP_PROXY 必须为有效 HTTP(S) 代理地址")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &Sender{Client: &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, TelegramBase: "https://api.telegram.org"}, nil
}

func DecodeChannel(kind string, raw json.RawMessage) (ChannelConfig, error) {
	var c ChannelConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, errors.New("渠道配置格式无效")
	}
	switch kind {
	case "telegram":
		if !regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`).MatchString(c.BotToken) || len(c.BotToken) > 256 || strings.TrimSpace(c.ChatID) == "" || len(c.ChatID) > 200 {
			return c, errors.New("Telegram 需要有效 bot_token 和 chat_id")
		}
		if c.URL != "" || c.Secret != "" {
			return c, errors.New("Telegram 不接受 Webhook 字段")
		}
	case "webhook":
		u, err := url.Parse(c.URL)
		if err != nil || len(c.URL) > 2048 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return c, errors.New("Webhook URL 必须为不含用户凭据或片段的 HTTP(S) 地址")
		}
		if len(c.Secret) > 1024 || c.BotToken != "" || c.ChatID != "" {
			return c, errors.New("Webhook 配置字段无效")
		}
	default:
		return c, errors.New("渠道 kind 必须为 telegram 或 webhook")
	}
	return c, nil
}

// RedactChannel never returns channel credentials. On edit, empty secret fields
// retain their previous values; clear_secret explicitly removes a webhook HMAC.
func RedactChannel(c store.NotifyChannel) map[string]any {
	var config ChannelConfig
	_ = json.Unmarshal(c.Config, &config)
	public := map[string]any{}
	if c.Kind == "telegram" {
		public["chat_id"] = config.ChatID
		public["has_bot_token"] = config.BotToken != ""
	} else {
		public["url"] = config.URL
		public["has_secret"] = config.Secret != ""
	}
	return map[string]any{"id": c.ID, "name": c.Name, "kind": c.Kind, "config": public, "enabled": c.Enabled, "created_at": c.CreatedAt}
}

func (s *Sender) Send(ctx context.Context, c store.NotifyChannel, e store.AlertEvent, recovery bool) error {
	config, err := DecodeChannel(c.Kind, c.Config)
	if err != nil {
		return err
	}
	title, level, message := e.Title, e.Level, e.Message
	if recovery {
		title = "已恢复：" + title
		level = "info"
		message = "告警条件已解除。\n" + message
	}
	stamp := e.FiredAt
	if recovery && e.ResolvedAt != nil {
		stamp = *e.ResolvedAt
	}
	var destination string
	var body []byte
	if c.Kind == "telegram" {
		content := fmt.Sprintf("[%s] %s\n%s\n时间：%s", level, title, message, time.Unix(stamp, 0).In(clock.Location()).Format("2006-01-02 15:04:05 MST"))
		body, _ = json.Marshal(map[string]any{"chat_id": config.ChatID, "parse_mode": "HTML", "text": html.EscapeString(content)})
		destination = strings.TrimRight(s.TelegramBase, "/") + "/bot" + config.BotToken + "/sendMessage"
	} else {
		body, _ = json.Marshal(map[string]any{"event": e, "level": level, "title": title, "message": message, "fired_at": e.FiredAt, "recovery": recovery})
		destination = config.URL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return errors.New("无法创建通知请求")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Kind == "webhook" && config.Secret != "" {
		mac := hmac.New(sha256.New, []byte(config.Secret))
		mac.Write(body)
		req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := s.Client.Do(req)
	if err != nil {
		return errors.New("通知请求失败（网络或超时）")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return errors.New("读取通知响应失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("通知接口 HTTP %d", response.StatusCode)
	}
	if c.Kind == "telegram" {
		var result struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(raw, &result) != nil || !result.OK {
			return errors.New("Telegram 未确认发送成功")
		}
	}
	return nil
}
