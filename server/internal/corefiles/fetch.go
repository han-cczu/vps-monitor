package corefiles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var ErrFetch = errors.New("从 GitHub 下载失败")
var releasePath = regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/download/[^/]+/[^/]+$`)

// Only GitHub Release assets are accepted. This feature cannot fetch arbitrary internal URLs.
func validFetchURL(u *url.URL, redirected bool) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	if u.Host == "github.com" {
		return u.RawQuery == "" && releasePath.MatchString(u.Path)
	}
	return redirected && u.Host == "release-assets.githubusercontent.com"
}
func FetchClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !validFetchURL(req.URL, true) {
			return errors.New("不允许的下载重定向")
		}
		return nil
	}}
}
func (s *Store) Fetch(ctx context.Context, client *http.Client, v, a, rawURL, checksum string) (Artifact, error) {
	u, e := url.Parse(rawURL)
	if e != nil || !validFetchURL(u, false) {
		return Artifact{}, fmt.Errorf("%w：只支持 HTTPS GitHub Release 文件链接", ErrInvalid)
	}
	if e = validate(v, a); e != nil {
		return Artifact{}, e
	}
	if !validHash(checksum) {
		return Artifact{}, fmt.Errorf("%w：SHA256 必须为 64 位十六进制", ErrInvalid)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return Artifact{}, e
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, e := client.Do(req)
	if e != nil {
		return Artifact{}, ErrFetch
	} // Do not leak signed redirect URLs in API errors.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Artifact{}, fmt.Errorf("%w（HTTP %d）", ErrFetch, resp.StatusCode)
	}
	if resp.ContentLength > MaxFileSize {
		return Artifact{}, ErrTooLarge
	}
	return s.Put(ctx, v, a, resp.Body, checksum)
}
