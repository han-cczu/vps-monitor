package corefiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vpsmon/server/internal/store"
	"vpsmon/server/internal/testutil"
)

const tags = "with_quic,with_utls,with_v2ray_api"

func checksum(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func newStore(t *testing.T) *Store {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = db.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	s, e := New(t.TempDir(), db)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	s.arch = "amd64"
	return s
}
func put(t *testing.T, s *Store, v, a string, data []byte) {
	t.Helper()
	if _, e := s.Put(context.Background(), v, a, bytes.NewReader(data), checksum(data)); e != nil {
		t.Fatal(e)
	}
}

func TestBinaryValidationAndVersionLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	x86 := testutil.CoreBinary(t, "v1.14.0", "amd64", tags)
	arm := testutil.CoreBinary(t, "v1.14.0", "arm64", tags)
	for _, tc := range []struct {
		name, v, a string
		data       []byte
		sum        string
	}{
		{"traversal", "../v1.14.0", "amd64", x86, checksum(x86)},
		{"arch", "v1.14.0", "arm64", x86, checksum(x86)},
		{"version", "v1.14.0-beta.1", "amd64", x86, checksum(x86)},
		{"checksum", "v1.14.0", "amd64", x86, strings.Repeat("0", 64)},
		{"not_binary", "v1.14.0", "amd64", []byte("file"), checksum([]byte("file"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := s.Put(ctx, tc.v, tc.a, bytes.NewReader(tc.data), tc.sum); !errors.Is(e, ErrInvalid) {
				t.Fatalf("got %v", e)
			}
		})
	}
	missingTag := testutil.CoreBinary(t, "v1.14.0", "amd64", "with_quic,with_utls")
	if _, e := s.Put(ctx, "v1.14.0", "amd64", bytes.NewReader(missingTag), checksum(missingTag)); !errors.Is(e, ErrInvalid) {
		t.Fatalf("missing tag accepted: %v", e)
	}
	put(t, s, "v1.14.0", "amd64", x86)
	if e := s.SetCurrent(ctx, "v1.14.0"); !errors.Is(e, ErrConflict) {
		t.Fatalf("incomplete activated: %v", e)
	}
	put(t, s, "v1.14.0", "arm64", arm)
	if e := s.SetCurrent(ctx, "v1.14.0"); e != nil {
		t.Fatal(e)
	}
	local, e := s.root.ReadFile("current-local")
	if e != nil || checksum(local) != checksum(x86) {
		t.Fatalf("local mismatch: %v", e)
	}
	versions, e := s.List(ctx)
	if e != nil || len(versions) != 1 || !versions[0].Current || len(versions[0].Arches) != 2 {
		t.Fatalf("list: %+v %v", versions, e)
	}
	if e := s.Delete(ctx, "v1.14.0"); !errors.Is(e, ErrConflict) {
		t.Fatalf("deleted current: %v", e)
	}
	put(t, s, "v1.14.0", "amd64", x86) // same bytes idempotent
	tampered := append(append([]byte(nil), x86...), 1)
	if _, e := s.Put(ctx, "v1.14.0", "amd64", bytes.NewReader(tampered), checksum(tampered)); !errors.Is(e, ErrConflict) {
		t.Fatalf("overwrite accepted: %v", e)
	}
	if e := s.root.WriteFile("current-local", []byte("interrupted"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	local, _ = s.root.ReadFile("current-local")
	if checksum(local) != checksum(x86) {
		t.Fatal("reconcile did not restore executable")
	}
	if e := s.root.WriteFile(filename("v1.14.0", "arm64"), []byte("corrupted"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := s.SetCurrent(ctx, "v1.14.0"); !errors.Is(e, ErrConflict) {
		t.Fatalf("corrupt core activated: %v", e)
	}
}

type failingSettings struct {
	Settings
	fail bool
}

func (s *failingSettings) SetSetting(ctx context.Context, k string, v any) error {
	if s.fail {
		return errors.New("test database unavailable")
	}
	return s.Settings.SetSetting(ctx, k, v)
}
func TestActivationFailureAndRestartRecovery(t *testing.T) {
	s := newStore(t)
	s.validateBinary = func(*os.File, string, string) error { return nil }
	ctx := context.Background()
	for _, v := range []string{"v1.14.0", "v1.14.1"} {
		for _, a := range []string{"amd64", "arm64"} {
			put(t, s, v, a, []byte(v+a))
		}
	}
	if e := s.SetCurrent(ctx, "v1.14.0"); e != nil {
		t.Fatal(e)
	}
	db := &failingSettings{Settings: s.db, fail: true}
	s.db = db
	if e := s.SetCurrent(ctx, "v1.14.1"); e == nil {
		t.Fatal("expected settings failure")
	}
	v, _ := s.current(ctx)
	local, _ := s.root.ReadFile("current-local")
	if v != "v1.14.0" || string(local) != "v1.14.0amd64" {
		t.Fatal("failed activation changed current")
	}
	db.fail = false
	if e := s.SetCurrent(ctx, "v1.14.1"); e != nil {
		t.Fatal(e)
	}
	if e := s.Delete(ctx, "v1.14.0"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Open("v1.14.0", "amd64"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	// Rename failure rolls back the selected version. Startup can retry from the DB.
	if e := s.root.Remove("current-local"); e != nil {
		t.Fatal(e)
	}
	if e := s.root.Mkdir("current-local", 0700); e != nil {
		t.Fatal(e)
	}
	for _, a := range []string{"amd64", "arm64"} {
		put(t, s, "v1.14.2", a, []byte("next"+a))
	}
	if e := s.SetCurrent(ctx, "v1.14.2"); e == nil {
		t.Fatal("expected rename failure")
	}
	v, _ = s.current(ctx)
	if v != "v1.14.1" {
		t.Fatalf("rollback current=%s", v)
	}
	if e := s.root.Remove("current-local"); e != nil {
		t.Fatal(e)
	}
	if e := s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestUploadLimitsCancellationAndConcurrentPublication(t *testing.T) {
	s := newStore(t)
	s.validateBinary = func(*os.File, string, string) error { return nil }
	ctx := context.Background()
	if _, e := s.Put(ctx, "v1.14.0", "amd64", io.LimitReader(zeroReader{}, MaxFileSize+1), strings.Repeat("0", 64)); !errors.Is(e, ErrTooLarge) {
		t.Fatalf("oversize: %v", e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.Put(cancelled, "v1.14.0", "amd64", strings.NewReader("x"), checksum([]byte("x"))); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel: %v", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, a := range []string{"amd64", "arm64"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Put(ctx, "v1.14.0", a, strings.NewReader(a), checksum([]byte(a)))
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	versions, e := s.List(ctx)
	if e != nil || len(versions) != 1 || len(versions[0].Files) != 2 {
		t.Fatalf("lost concurrent publication: %+v %v", versions, e)
	}
	sums, e := s.root.ReadFile("v1.14.0/SHA256SUMS")
	if e != nil || !strings.Contains(string(sums), checksum([]byte("amd64"))) || !strings.Contains(string(sums), checksum([]byte("arm64"))) {
		t.Fatalf("SUMS: %s %v", sums, e)
	}
	entries, _ := os.ReadDir(s.root.Name())
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatal("temporary upload leaked")
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFetchRestrictionsAndStreaming(t *testing.T) {
	for _, raw := range []string{"http://github.com/a/b/releases/download/v1/file", "https://localhost/core", "https://127.0.0.1/core", "https://github.com.evil.example/a/b/releases/download/v1/file", "https://user:pass@github.com/a/b/releases/download/v1/file", "https://github.com:443/a/b/releases/download/v1/file", "https://github.com/a/b/blob/main/file", "https://release-assets.githubusercontent.com/file"} {
		u, _ := url.Parse(raw)
		if validFetchURL(u, false) {
			t.Fatalf("accepted %s", raw)
		}
	}
	client := FetchClient()
	req, _ := http.NewRequest("GET", "https://localhost/secret", nil)
	if e := client.CheckRedirect(req, []*http.Request{{}}); e == nil {
		t.Fatal("internal redirect accepted")
	}
	req, _ = http.NewRequest("GET", "https://release-assets.githubusercontent.com/asset?signature=x", nil)
	if e := client.CheckRedirect(req, []*http.Request{{}}); e != nil {
		t.Fatal(e)
	}
	s := newStore(t)
	s.validateBinary = func(*os.File, string, string) error { return nil }
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("credential forwarded")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("core")), ContentLength: -1, Header: http.Header{}}, nil
	})
	if _, e := s.Fetch(context.Background(), client, "v1.14.0", "amd64", "https://github.com/owner/repo/releases/download/sing-box-v1.14.0/file", checksum([]byte("core"))); e != nil {
		t.Fatal(e)
	}
}
