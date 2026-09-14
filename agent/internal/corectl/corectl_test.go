package corectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
)

type fakeRunner struct {
	mu      sync.Mutex
	running bool
	calls   []string
	hook    func(context.Context, string, []string) (string, error, bool)
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("command has no deadline")
	}
	f.mu.Lock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	if f.hook != nil {
		if out, err, ok := f.hook(ctx, name, args); ok {
			return out, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "systemctl" {
		switch args[0] {
		case "is-active":
			if f.running {
				return "active", nil
			}
			return "inactive", nil
		case "restart", "start":
			f.running = true
		case "stop":
			f.running = false
		}
		return "", nil
	}
	if name == "ufw" || name == "firewall-cmd" {
		return "", os.ErrNotExist
	}
	if len(args) > 0 && args[0] == "version" {
		return "sing-box version 1.14.0\n", nil
	}
	return "", nil
}
func fixture(t *testing.T) (*Manager, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	p := Paths{Binary: filepath.Join(dir, "bin", "sing-box"), Config: filepath.Join(dir, "etc", "config.json"), Log: filepath.Join(dir, "log", "box.log"), State: filepath.Join(dir, "state", "core.json"), Unit: filepath.Join(dir, "unit", "sing-box.service"), Proc: dir}
	f := &fakeRunner{running: true}
	m, err := New(Options{Paths: p, Runner: f})
	if err != nil {
		t.Fatal(err)
	}
	m.healthTimeout = 40 * time.Millisecond
	m.healthInterval = time.Millisecond
	m.listening = func() ([]string, error) { return []string{"12345/tcp"}, nil }
	if err = atomicWrite(p.Binary, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, f
}
func applyMessage(t *testing.T, revision int64, raw string) proto.CoreApply {
	t.Helper()
	b, sum, err := CompactConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return proto.CoreApply{Type: proto.TypeCoreApply, Core: "sing-box", Revision: revision, Version: "v1.14.0", ConfigSHA256: sum, Config: b, Ports: []string{"12345/tcp"}, ReqID: "apply-1"}
}
func seed(t *testing.T, m *Manager) proto.CoreApply {
	t.Helper()
	a := applyMessage(t, 1, `{"old":true}`)
	if err := atomicWrite(m.paths.Config, a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.save(record{InstalledVersion: a.Version, AppliedRevision: 1, ConfigSHA256: a.ConfigSHA256, Ports: a.Ports}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestValidate(t *testing.T) {
	a := applyMessage(t, 1, ` { "ok": true } `)
	if err := Validate(a); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*proto.CoreApply){
		"type": func(a *proto.CoreApply) { a.Type = "exec" }, "core": func(a *proto.CoreApply) { a.Core = "other" }, "revision": func(a *proto.CoreApply) { a.Revision = 0 },
		"version": func(a *proto.CoreApply) { a.Version = "../../bin" }, "hash": func(a *proto.CoreApply) { a.ConfigSHA256 = strings.Repeat("0", 64) },
		"array": func(a *proto.CoreApply) { a.Config = []byte(`[]`) }, "null": func(a *proto.CoreApply) { a.Config = []byte(`null`) }, "invalid": func(a *proto.CoreApply) { a.Config = []byte(`{`) },
		"large": func(a *proto.CoreApply) { a.Config = []byte(strings.Repeat(" ", MaxConfigSize)) }, "zero port": func(a *proto.CoreApply) { a.Ports = []string{"0/tcp"} },
		"large port": func(a *proto.CoreApply) { a.Ports = []string{"65536/udp"} }, "shell": func(a *proto.CoreApply) { a.Ports = []string{"443/tcp;id"} },
		"leading zero": func(a *proto.CoreApply) { a.Ports = []string{"0443/tcp"} }, "dup": func(a *proto.CoreApply) { a.Ports = []string{"443/tcp", "443/tcp"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := a
			mutate(&b)
			if Validate(b) == nil {
				t.Fatal("accepted invalid message")
			}
		})
	}
	a.Ports = []string{"1/tcp", "65535/udp"}
	if err := Validate(a); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"localhost:10085", "0.0.0.0:10085", "8.8.8.8:10085", "127.0.0.1:0", "[::1]:65536"} {
		if ValidateStatsAddress(addr) == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:10085", "[::1]:10085"} {
		if err := ValidateStatsAddress(addr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyRollbackAndCommit(t *testing.T) {
	for _, mode := range []string{"success", "check", "missing", "no-backup", "firewall", "restart"} {
		t.Run(mode, func(t *testing.T) {
			m, f := fixture(t)
			var old proto.CoreApply
			if mode != "no-backup" {
				old = seed(t, m)
			}
			a := applyMessage(t, 2, `{"new":true}`)
			if mode == "missing" || mode == "no-backup" {
				a.Ports = []string{"54321/tcp"}
			}
			failed := false
			f.hook = func(_ context.Context, name string, args []string) (string, error, bool) {
				if mode == "check" && args[0] == "check" {
					return "secret-password", errors.New("bad config"), true
				}
				if mode == "firewall" && name == "ufw" {
					if args[0] == "status" {
						return "Status: active", nil, true
					}
					return "", errors.New("denied"), true
				}
				if mode == "restart" && name == "systemctl" && args[0] == "restart" && !failed {
					failed = true
					return "", errors.New("restart failed"), true
				}
				return "", nil, false
			}
			st, err := m.Execute(context.Background(), a)
			if mode == "success" {
				if err != nil || st.AppliedRevision != 2 || !st.Running || st.ReqID != a.ReqID {
					t.Fatalf("state=%+v err=%v", st, err)
				}
				before := len(f.calls)
				if _, err = m.Execute(context.Background(), a); err != nil {
					t.Fatal(err)
				}
				for _, call := range f.calls[before:] {
					if strings.Contains(call, " check ") || strings.Contains(call, " restart ") {
						t.Fatalf("idempotent apply mutated service: %s", call)
					}
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				if strings.Contains(err.Error(), "secret-password") {
					t.Fatal("leaked checker output")
				}
				if st.AppliedRevision != old.Revision {
					t.Fatalf("revision advanced on failure: %+v", st)
				}
				got, readErr := os.ReadFile(m.paths.Config)
				if mode == "no-backup" {
					if !os.IsNotExist(readErr) || st.Running {
						t.Fatalf("first apply failure did not stop/remove: %v %+v", readErr, st)
					}
				} else if readErr != nil || string(got) != string(old.Config) || !st.Running {
					t.Fatalf("old service/config lost: %s %v %+v", got, readErr, st)
				}
			}
			if _, err = os.Stat(m.paths.journal()); !os.IsNotExist(err) {
				t.Fatalf("journal remains: %v", err)
			}
			if _, err = os.Stat(m.paths.Config + ".new"); !os.IsNotExist(err) {
				t.Fatal("new config remains")
			}
		})
	}
}

func TestRejectStaleAndVersionMismatch(t *testing.T) {
	m, f := fixture(t)
	seed(t, m)
	for _, a := range []proto.CoreApply{applyMessage(t, 1, `{"conflict":true}`), func() proto.CoreApply { a := applyMessage(t, 2, `{}`); a.Version = "v1.13.0"; return a }()} {
		if _, err := m.Execute(context.Background(), a); err == nil {
			t.Fatal("accepted stale version/revision")
		}
	}
	for _, call := range f.calls {
		if strings.Contains(call, " restart ") || strings.Contains(call, " check ") {
			t.Fatalf("unexpected mutation %s", call)
		}
	}
}

func TestRecoverInterruptedApply(t *testing.T) {
	m, _ := fixture(t)
	old := seed(t, m)
	j := applyJournal{Previous: m.snapshot(), HadConfig: true}
	if err := atomicWrite(m.paths.backup(), old.Config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(m.paths.journal(), j); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(m.paths.Config, []byte(`{"interrupted":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.withLock(context.Background(), func() error { return m.recoverApply(context.Background()) }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(m.paths.Config)
	if string(b) != string(old.Config) || m.AppliedRevision() != 1 {
		t.Fatalf("bad recovery %s", b)
	}
}

func TestProcessLockExcludesOtherManager(t *testing.T) {
	m, _ := fixture(t)
	seed(t, m)
	unlock, err := fileLock(m.paths.State + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = m.Execute(context.Background(), applyMessage(t, 2, `{}`))
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("lock ignored: %v", err)
	}
}

func TestMessageAllowlistAndQueue(t *testing.T) {
	m, _ := fixture(t)
	for _, raw := range []string{`{"type":"exec","command":"id"}`, `{"type":"core.action","action":"start","command":"id"}`, `{"type":"core.logs","kind":"access","lines":5}`, `{"type":"core.action","action":"install","version":"../../x"}`, `{"type":"core.action","action":"restart"} {}`, `{"type":"core.action","action":"restart","version":"v1.14.0"}`} {
		if m.Handle([]byte(raw)) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for i := 0; i < 4; i++ {
		if err := m.Handle([]byte(`{"type":"core.action","action":"stop"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Handle([]byte(`{"type":"core.action","action":"stop"}`)); err == nil {
		t.Fatal("unbounded queue")
	}
}

func TestInstallDownloadAndRollback(t *testing.T) {
	for _, mode := range []string{"ok", "hash", "enable", "redirect", "version"} {
		t.Run(mode, func(t *testing.T) {
			m, f := fixture(t)
			seed(t, m)
			body := []byte("downloaded-core")
			sum := sha256.Sum256(body)
			leaked := false
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
			defer other.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer node-token" || r.URL.Path != "/api/agent/corefiles/v1.14.0/"+runtime.GOARCH {
					t.Errorf("unexpected download request %s", r.URL.Path)
				}
				if mode == "redirect" {
					http.Redirect(w, r, other.URL, 302)
					return
				}
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			m.server = "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agent/ws"
			m.token = "node-token"
			a := proto.CoreAction{Type: proto.TypeCoreAction, Action: "install", Version: "v1.14.0", SHA256: hex.EncodeToString(sum[:])}
			if mode == "hash" {
				a.SHA256 = strings.Repeat("0", 64)
			}
			f.hook = func(_ context.Context, name string, args []string) (string, error, bool) {
				if mode == "enable" && name == "systemctl" && args[0] == "enable" {
					return "", errors.New("enable failed"), true
				}
				if mode == "version" && args[0] == "version" {
					return "sing-box version 1.13.0", nil, true
				}
				return "", nil, false
			}
			_, err := m.Execute(context.Background(), a)
			b, _ := os.ReadFile(m.paths.Binary)
			if mode == "ok" {
				if err != nil || string(b) != string(body) {
					t.Fatalf("install failed %s %v", b, err)
				}
			} else if err == nil || string(b) != "old-binary" {
				t.Fatalf("damaged old binary %s %v", b, err)
			}
			if leaked {
				t.Fatal("followed download redirect")
			}
		})
	}
}

func TestPortsAndLogs(t *testing.T) {
	sample := `  sl  local_address rem_address st
0: 0100007F:01BB 00000000:0000 0A
1: 0100007F:1F90 00000000:0000 01
2: 00000000000000000000000001000000:01BB 00000000:0000 0A
3: invalid 00000000:0000 0A
`
	tcp, err := ParsePorts(strings.NewReader(sample), "tcp")
	if err != nil || !slices.Equal(tcp, []string{"443/tcp"}) {
		t.Fatalf("tcp=%v %v", tcp, err)
	}
	udp, _ := ParsePorts(strings.NewReader(sample), "udp")
	if !slices.Equal(udp, []string{"443/udp", "8080/udp"}) {
		t.Fatal(udp)
	}
	if got := Missing([]string{"443/tcp", "80/tcp"}, tcp); !slices.Equal(got, []string{"80/tcp"}) {
		t.Fatal(got)
	}
	p := filepath.Join(t.TempDir(), "box.log")
	raw := ""
	for i := 0; i < 1200; i++ {
		raw += fmt.Sprintf("行%d\n", i)
	}
	if err = os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	tail, err := Tail(p, 2)
	if err != nil || tail != "行1198\n行1199" {
		t.Fatalf("tail=%q %v", tail, err)
	}
	if _, err = Tail(p, 1001); err == nil {
		t.Fatal("unbounded lines")
	}
	if err = os.WriteFile(p, []byte(strings.Repeat("x", 2*maxLogBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	tail, err = Tail(p, 1)
	if err != nil || len(tail) > maxLogBytes {
		t.Fatal("unbounded log bytes")
	}
}

func TestFirewallCommands(t *testing.T) {
	for _, kind := range []string{"ufw", "firewalld", "none"} {
		t.Run(kind, func(t *testing.T) {
			m, f := fixture(t)
			f.hook = func(_ context.Context, name string, args []string) (string, error, bool) {
				if kind == "ufw" && name == "ufw" {
					if args[0] == "status" {
						return "Status: active", nil, true
					}
					return "", nil, true
				}
				if kind == "firewalld" && name == "firewall-cmd" {
					if args[0] == "--state" {
						return "running", nil, true
					}
					return "", nil, true
				}
				return "", nil, false
			}
			got, err := m.firewall(context.Background(), []string{"443/tcp", "8443/udp"})
			if err != nil || got != kind {
				t.Fatalf("%s %v", got, err)
			}
			calls := strings.Join(f.calls, "\n")
			if kind == "ufw" && !strings.Contains(calls, "ufw allow 8443/udp") {
				t.Fatal(calls)
			}
			if kind == "firewalld" && (!strings.Contains(calls, "--permanent --add-port=443/tcp") || !strings.Contains(calls, "--reload")) {
				t.Fatal(calls)
			}
		})
	}
}

func TestMonitorReportsCrashAndStops(t *testing.T) {
	m, f := fixture(t)
	seed(t, m)
	m.pollInterval = 5 * time.Millisecond
	m.statsInterval = time.Hour
	states := make(chan proto.CoreState, 20)
	m.send = func(v any) error {
		if st, ok := v.(proto.CoreState); ok {
			states <- st
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("manager did not stop")
		}
	}()
	select {
	case st := <-states:
		if !st.Running {
			t.Fatal("initial state not running")
		}
	case <-time.After(time.Second):
		t.Fatal("no initial state")
	}
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
	select {
	case st := <-states:
		if st.Running {
			t.Fatal("crash not detected")
		}
	case <-time.After(time.Second):
		t.Fatal("no crash state")
	}
}

func TestStateReloadFromDiskAndPermissions(t *testing.T) {
	m, _ := fixture(t)
	old := seed(t, m)
	r, err := readRecord(m.paths.State)
	if err != nil || r.ConfigSHA256 != old.ConfigSHA256 {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" {
		st, _ := os.Stat(m.paths.State)
		if st.Mode().Perm() != 0600 {
			t.Fatalf("state permissions %v", st.Mode())
		}
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "old-binary") {
		t.Fatal("binary stored in state")
	}
}

func TestRecoveryRevisionDoesNotAdvanceBeforeCommit(t *testing.T) {
	m, _ := fixture(t)
	seed(t, m)
	old := m.snapshot()
	if err := writeJSON(m.paths.journal(), applyJournal{Previous: old, HadConfig: true}); err != nil {
		t.Fatal(err)
	}
	next := old
	next.AppliedRevision = 2
	if err := m.save(next); err != nil {
		t.Fatal(err)
	}
	if m.AppliedRevision() != 1 || m.State(context.Background()).AppliedRevision != 1 {
		t.Fatal("uncommitted revision leaked into hello/state")
	}
}

func TestExplicitRestartClearsStartLimit(t *testing.T) {
	m, f := fixture(t)
	limited := true
	f.hook = func(_ context.Context, name string, args []string) (string, error, bool) {
		if name == "systemctl" {
			if args[0] == "reset-failed" {
				limited = false
			}
			if args[0] == "restart" && limited {
				return "", errors.New("start-limit-hit"), true
			}
		}
		return "", nil, false
	}
	if err := m.service(context.Background(), "restart"); err != nil {
		t.Fatal(err)
	}
	if limited {
		t.Fatal("did not reset the failed unit")
	}
}

func TestCancelledApplyRollsBackAndReportsActualState(t *testing.T) {
	m, f := fixture(t)
	old := seed(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := true
	f.hook = func(ctx context.Context, name string, args []string) (string, error, bool) {
		if ctx.Err() != nil {
			return "", ctx.Err(), true
		}
		if name == "systemctl" && args[0] == "restart" && first {
			first = false
			cancel()
		}
		return "", nil, false
	}
	st, err := m.Execute(ctx, applyMessage(t, 2, `{"new":true}`))
	if err == nil || !st.Running || st.AppliedRevision != 1 || st.ConfigSHA256 != old.ConfigSHA256 {
		t.Fatalf("cancel lost restored state: %+v %v", st, err)
	}
	b, _ := os.ReadFile(m.paths.Config)
	if string(b) != string(old.Config) {
		t.Fatalf("old config lost: %s", b)
	}
}

func TestMissingCommandAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (execRunner{}).Run(ctx, "no-such-corectl-command"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := (execRunner{}).Run(ctx, "no-such-corectl-command"); err == nil {
		t.Fatal("missing command not reported")
	}
}

func TestSingleWorkerAndCorrelatedReplies(t *testing.T) {
	m, f := fixture(t)
	seed(t, m)
	m.statsInterval = time.Hour
	entered := make(chan struct{})
	release := make(chan struct{})
	started := false
	f.hook = func(_ context.Context, name string, args []string) (string, error, bool) {
		if name == "systemctl" && args[0] == "restart" && !started {
			started = true
			close(entered)
			<-release
		}
		return "", nil, false
	}
	replies := make(chan string, 10)
	m.send = func(v any) error {
		if st, ok := v.(proto.CoreState); ok && st.ReqID != "" {
			replies <- st.ReqID
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-m.Ready()
	if err := m.Handle([]byte(`{"type":"core.action","action":"restart","req_id":"first"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker never started")
	}
	if err := m.Handle([]byte(`{"type":"core.action","action":"stop","req_id":"second"}`)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	calls := strings.Join(f.calls, "\n")
	f.mu.Unlock()
	if strings.Contains(calls, "systemctl stop ") {
		t.Fatal("operations overlapped")
	}
	close(release)
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-replies:
			if got != want {
				t.Fatalf("reply=%s want %s", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("reply missing")
		}
	}
}
