// Package updates checks public releases without downloading or installing software.
package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"vpsmon/proto"
)

const DefaultRepository = "han-cczu/vps-monitor"

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

type Release struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// Result 是检查更新对外暴露的全部状态。
//
// CheckedAt 记录最近一次检查的时间（成功或失败都算）；Latest 只在检查成功时更新，
// 查询失败时保留上一次成功的结果，所以调用方必须靠 State 判断新鲜度，
// 不能把 Latest 非空当成"刚刚确认过"。
type Result struct {
	Repository  string   `json:"repository"`
	State       string   `json:"state"` // unchecked, ok, error
	CheckedAt   int64    `json:"checked_at"`
	NextCheckAt int64    `json:"next_check_at"`
	Latest      *Release `json:"latest"`
	Message     string   `json:"message"`
}

type Checker struct {
	repository string
	client     *http.Client
	now        func() time.Time
	gate       chan struct{}
	mu         sync.Mutex
	result     Result
}

// New 构造检查器，仓库为空时用项目仓库，客户端固定超时且不跟随重定向。
func New(repository string) (*Checker, error) {
	return NewWithClient(repository, nil)
}

// NewWithClient 允许注入 HTTP 客户端：测试用它拦截网络，部署时可以换成为更新源准备的代理。
// client 为 nil 时使用默认客户端。
func NewWithClient(repository string, client *http.Client) (*Checker, error) {
	if repository == "" {
		repository = DefaultRepository
	}
	if !repositoryPattern.MatchString(repository) {
		return nil, fmt.Errorf("VM_UPDATE_REPOSITORY 必须是 GitHub 的 owner/repo 格式")
	}
	if client == nil {
		client = &http.Client{
			Timeout: 12 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("更新源发生重定向，请核对 VM_UPDATE_REPOSITORY")
			},
		}
	}
	return &Checker{
		repository: repository,
		client:     client,
		now:        time.Now, gate: make(chan struct{}, 1),
		result: Result{Repository: repository, State: "unchecked"},
	}, nil
}

func (c *Checker) Snapshot() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.result
	if result.Latest != nil {
		latest := *result.Latest
		result.Latest = &latest
	}
	return result
}

// Check coalesces concurrent requests and caches successes for ten minutes and
// failures for one minute. Opening a page only reads Snapshot; it never calls GitHub.
func (c *Checker) Check(ctx context.Context) Result {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		// 排队期间的取消只影响这一个请求：既不动缓存，也不谎称拿到了结果。
		return c.failure("检查更新已取消，请重试", c.Snapshot(), c.now())
	}
	// 拿到锁之后再读缓存：合并进来的并发请求在这里就能命中刚写好的结果。
	previous := c.Snapshot()
	if c.now().Unix() < previous.NextCheckAt {
		return previous
	}
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	latest, err := c.fetch(checkCtx)
	now := c.now()
	result := Result{Repository: c.repository, State: "ok", Latest: latest, CheckedAt: now.Unix(), NextCheckAt: now.Add(10 * time.Minute).Unix()}
	if err != nil {
		result = c.failure(err.Error(), previous, now)
	}
	// A disconnected browser must not replace a useful cached result with its cancellation.
	if ctx.Err() == nil {
		c.mu.Lock()
		c.result = result
		c.mu.Unlock()
	}
	return result
}

// failure 构造一次检查失败的结果：保留上一次成功拿到的最新版本供界面展示，
// 状态与失败时间如实反映本次尝试，重试窗口缩短到一分钟。
func (c *Checker) failure(message string, previous Result, now time.Time) Result {
	return Result{
		Repository:  c.repository,
		State:       "error",
		CheckedAt:   now.Unix(),
		NextCheckAt: now.Add(time.Minute).Unix(),
		Latest:      previous.Latest,
		Message:     message,
	}
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	var latest *Release
	// The repository also publishes sing-box releases. Scan stable vX.Y.Z tags,
	// comparing numerically instead of trusting release order or /releases/latest.
	for page := 1; page <= 3; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", c.repository, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "vps-monitor-update-check")
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("无法连接 GitHub 更新源，请检查面板服务器网络后重试")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			return nil, fmt.Errorf("更新仓库不存在或不是公开仓库，请核对更新源")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, fmt.Errorf("GitHub 拒绝请求或已限流，请稍后重试")
		default:
			return nil, fmt.Errorf("GitHub 更新源暂时不可用（HTTP %d）", resp.StatusCode)
		}
		if readErr != nil || len(raw) > 2<<20 {
			return nil, fmt.Errorf("更新源响应不完整或过大，请稍后重试")
		}
		var releases []struct {
			Tag         string `json:"tag_name"`
			Draft       bool   `json:"draft"`
			Prerelease  bool   `json:"prerelease"`
			PublishedAt string `json:"published_at"`
		}
		if err := json.Unmarshal(raw, &releases); err != nil || strings.TrimSpace(string(raw)) == "null" {
			return nil, fmt.Errorf("更新源返回了无效的版本信息")
		}
		for _, r := range releases {
			if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, "v") || !proto.ValidAgentVersion(r.Tag) {
				continue
			}
			if latest == nil || proto.AgentVersionNewer(r.Tag, latest.Version) {
				latest = &Release{Version: r.Tag, URL: "https://github.com/" + c.repository + "/releases/tag/" + r.Tag, PublishedAt: r.PublishedAt}
			}
		}
		if !strings.Contains(resp.Header.Get("Link"), `rel="next"`) {
			return latest, nil
		}
	}
	return nil, fmt.Errorf("发布记录过多，无法确认最新版本，请到发布页核对")
}

// Compare deliberately keeps custom/dev/prerelease builds unranked.
func Compare(latest, current string) string {
	if !proto.ValidAgentVersion(current) || !proto.ValidAgentVersion(latest) {
		return "unknown"
	}
	if proto.AgentVersionNewer(latest, current) {
		return "available"
	}
	if proto.AgentVersionNewer(current, latest) {
		return "ahead"
	}
	return "current"
}
