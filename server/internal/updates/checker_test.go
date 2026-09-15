package updates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func fixture(t *testing.T, f transportFunc) (*Checker, *time.Time) {
	t.Helper()
	c, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	c.now = func() time.Time { return now }
	c.client.Transport = f
	return c, &now
}

func TestStableReleasesPaginationAndCache(t *testing.T) {
	calls := 0
	c, now := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/"+DefaultRepository+"/releases" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Accept") == "" || r.Header.Get("User-Agent") == "" || r.URL.Query().Get("per_page") != "100" {
			t.Fatal("missing request metadata")
		}
		if r.URL.Query().Get("page") == "1" {
			resp := response(200, `[{"tag_name":"sing-box-v99.0.0"},{"tag_name":"v99.0.0","draft":true},{"tag_name":"v90.0.0","prerelease":true},{"tag_name":"v9.0.0-rc.1"},{"tag_name":"v0.9.0"}]`)
			// Never follow an upstream pagination URL; construct the next fixed-host request.
			resp.Header.Set("Link", `<https://evil.invalid/private>; rel="next"`)
			return resp, nil
		}
		return response(200, `[{"tag_name":"v0.10.0","html_url":"javascript:evil","published_at":"2026-09-15T00:00:00Z"},{"tag_name":"v0.2.0"}]`), nil
	})
	if s := c.Snapshot(); s.State != "unchecked" || calls != 0 {
		t.Fatalf("snapshot initiated a check: %+v", s)
	}
	s := c.Check(context.Background())
	if s.State != "ok" || s.Latest == nil || s.Latest.Version != "v0.10.0" || calls != 2 || s.Latest.URL != "https://github.com/"+DefaultRepository+"/releases/tag/v0.10.0" {
		t.Fatalf("bad release selection: %+v calls=%d", s, calls)
	}
	c.Check(context.Background())
	if calls != 2 {
		t.Fatal("cache miss")
	}
	copy := c.Snapshot()
	copy.Latest.Version = "mutated"
	if c.Snapshot().Latest.Version != "v0.10.0" {
		t.Fatal("snapshot aliases cached release")
	}
	*now = now.Add(10 * time.Minute)
	c.Check(context.Background())
	if calls != 4 {
		t.Fatal("cache did not expire")
	}
}

func TestNoReleaseAndFailuresStayDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, body, state, contains string
		code                        int
	}{
		{"no release", `[]`, "ok", "", 200},
		{"only core", `[{"tag_name":"sing-box-v1.14.0"}]`, "ok", "", 200},
		{"not found", `[]`, "error", "不存在", 404},
		{"limited", `{}`, "error", "限流", 429},
		{"forbidden", `{}`, "error", "拒绝", 403},
		{"unavailable", `oops`, "error", "503", 503},
		{"malformed", `{`, "error", "无效", 200},
		{"null", `null`, "error", "无效", 200},
		{"oversized", strings.Repeat(" ", (2<<20)+1), "error", "过大", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, now := fixture(t, func(*http.Request) (*http.Response, error) { calls++; return response(tc.code, tc.body), nil })
			s := c.Check(context.Background())
			if s.State != tc.state || s.Latest != nil || !strings.Contains(s.Message, tc.contains) {
				t.Fatalf("bad result %+v", s)
			}
			c.Check(context.Background())
			if calls != 1 {
				t.Fatal("result not cached")
			}
			if tc.state == "error" {
				*now = now.Add(time.Minute)
				c.Check(context.Background())
				if calls != 2 {
					t.Fatal("error retry remained blocked")
				}
			}
		})
	}
}

func TestConcurrentChecksShareRequest(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	c, _ := fixture(t, func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return response(200, `[{"tag_name":"v1.0.0"}]`), nil
	})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if s := c.Check(context.Background()); s.State != "ok" {
				t.Errorf("check: %+v", s)
			}
		})
	}
	<-started
	if c.Snapshot().State != "unchecked" {
		t.Fatal("pending check changed snapshot")
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicated requests: %d", calls.Load())
	}
}

func TestTimeoutCancellationAndScanLimit(t *testing.T) {
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("unbounded request")
		}
		return nil, context.DeadlineExceeded
	})
	if s := c.Check(context.Background()); s.State != "error" || s.Latest != nil {
		t.Fatal(s)
	}
	c, _ = fixture(t, func(r *http.Request) (*http.Response, error) { return nil, errors.New("network failed") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Check(ctx)
	if c.Snapshot().State != "unchecked" {
		t.Fatal("cancellation poisoned cache")
	}
	calls := 0
	c, _ = fixture(t, func(*http.Request) (*http.Response, error) {
		calls++
		r := response(200, `[{"tag_name":"v1.0.0"}]`)
		r.Header.Set("Link", `<https://api.github.com/next>; rel="next"`)
		return r, nil
	})
	if s := c.Check(context.Background()); s.State != "error" || s.Latest != nil || calls != 3 {
		t.Fatalf("partial scan claimed latest: %+v calls=%d", s, calls)
	}
}

func TestFailureKeepsLastSuccessfulRelease(t *testing.T) {
	broken := false
	calls := 0
	c, now := fixture(t, func(*http.Request) (*http.Response, error) {
		calls++
		if broken {
			return response(503, `oops`), nil
		}
		return response(200, `[{"tag_name":"v1.2.0","published_at":"2026-09-01T00:00:00Z"}]`), nil
	})
	first := c.Check(context.Background())
	if first.State != "ok" || first.Latest == nil || first.Latest.Version != "v1.2.0" {
		t.Fatalf("initial check: %+v", first)
	}
	broken = true
	*now = now.Add(10 * time.Minute)
	second := c.Check(context.Background())
	if second.State != "error" || second.Message == "" {
		t.Fatalf("failure reported as success: %+v", second)
	}
	if second.Latest == nil || second.Latest.Version != "v1.2.0" {
		t.Fatalf("failure dropped the last known release: %+v", second)
	}
	if second.CheckedAt != now.Unix() || second.NextCheckAt != now.Add(time.Minute).Unix() {
		t.Fatalf("failure timestamps wrong: %+v", second)
	}
	if s := c.Snapshot(); s.State != "error" || s.Latest == nil || s.Latest.Version != "v1.2.0" {
		t.Fatalf("cache after failure: %+v", s)
	}
	if calls != 2 {
		t.Fatalf("unexpected upstream calls: %d", calls)
	}
	// 失败后一分钟内不再打上游，但仍如实返回失败状态。
	c.Check(context.Background())
	if calls != 2 {
		t.Fatal("failure retry window ignored")
	}
	broken = false
	*now = now.Add(time.Minute)
	if third := c.Check(context.Background()); third.State != "ok" || third.Latest.Version != "v1.2.0" {
		t.Fatalf("recovery: %+v", third)
	}
}

func TestPartialScanNeverClaimsLatest(t *testing.T) {
	c, _ := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("page") == "1" {
			resp := response(200, `[{"tag_name":"v1.2.0"}]`)
			resp.Header.Set("Link", `<https://api.github.com/repos/o/r/releases?page=2>; rel="next"`)
			return resp, nil
		}
		return response(503, `oops`), nil
	})
	s := c.Check(context.Background())
	if s.State != "error" || s.Latest != nil {
		t.Fatalf("half-scanned result claimed a latest release: %+v", s)
	}
}

func TestCancellationWhileQueuedLeavesCacheAlone(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c, _ := fixture(t, func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return response(200, `[{"tag_name":"v1.0.0"}]`), nil
	})
	done := make(chan Result, 1)
	go func() { done <- c.Check(context.Background()) }()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queued := c.Check(ctx)
	if queued.State != "error" || !strings.Contains(queued.Message, "取消") || queued.Latest != nil {
		t.Fatalf("queued cancellation: %+v", queued)
	}
	if s := c.Snapshot(); s.State != "unchecked" {
		t.Fatalf("queued cancellation poisoned the cache: %+v", s)
	}
	close(release)
	if s := <-done; s.State != "ok" || s.Latest == nil || s.Latest.Version != "v1.0.0" {
		t.Fatalf("in-flight check: %+v", s)
	}
}

func TestRepositoryAndVersionComparison(t *testing.T) {
	c, err := NewWithClient("", nil)
	if err != nil || c.repository != DefaultRepository || c.client.Timeout != 12*time.Second {
		t.Fatalf("default repository or client: %+v %v", c, err)
	}
	if err := c.client.CheckRedirect(&http.Request{}, nil); err == nil {
		t.Fatal("default client follows redirects")
	}
	injected := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `[]`), nil
	})}
	if c, err := NewWithClient("owner/repo", injected); err != nil || c.client != injected {
		t.Fatalf("injected client ignored: %+v %v", c, err)
	}

	for _, repository := range []string{"https://evil.invalid/a/b", "../repo", "owner/repo?token=x", "owner/repo/extra"} {
		if _, err := New(repository); err == nil {
			t.Fatalf("accepted %q", repository)
		}
	}
	for _, tc := range [][3]string{
		{"v0.10.0", "v0.9.0", "available"},
		{"v1.0.0", "1.0.0", "current"},
		{"v1.0.0", "v1.0.1", "ahead"},
		{"v1.0.0", "dev", "unknown"},
		{"v1.0.0", "v0.2.0-observe21", "unknown"},
		{"", "v1.0.0", "unknown"},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			if got := Compare(tc[0], tc[1]); got != tc[2] {
				t.Fatalf("got %s", got)
			}
		})
	}
}
