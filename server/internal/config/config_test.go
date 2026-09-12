package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearEnv 把所有 VM_* 变量清空，让用例从干净的默认值起步。
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"VM_LISTEN", "VM_DATA_DIR", "VM_PUBLIC_URL",
		"VM_JWT_SECRET", "VM_TZ", "VM_LOG_LEVEL", "VM_TRUSTED_PROXIES",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" || cfg.DataDir != "./data" || cfg.TZ != "Asia/Shanghai" || cfg.LogLevel != "info" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.PublicURL != "" || cfg.JWTSecret != "" {
		t.Fatalf("expected empty PublicURL/JWTSecret: %+v", cfg)
	}
	if !cfg.TrustedProxies.Direct() {
		t.Fatalf("default must be direct (no proxy headers trusted): %+v", cfg.TrustedProxies)
	}
	if got := cfg.DBPath(); got != filepath.Join("./data", "vm.db") {
		t.Fatalf("DBPath=%q", got)
	}
	if _, err := cfg.Location(); err != nil {
		t.Fatalf("Location: %v", err)
	}
}

func TestLoadTrimsAndValidates(t *testing.T) {
	clearEnv(t)
	t.Setenv("VM_PUBLIC_URL", "  https://panel.example.com/  ")
	t.Setenv("VM_JWT_SECRET", "  "+strings.Repeat("k", 32)+"  ")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://panel.example.com" {
		t.Fatalf("PublicURL=%q (trailing slash and spaces must go)", cfg.PublicURL)
	}
	if cfg.JWTSecret != strings.Repeat("k", 32) {
		t.Fatalf("JWTSecret not trimmed: %q", cfg.JWTSecret)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string][2]string{
		"bad log level":        {"VM_LOG_LEVEL", "chatty"},
		"public url no scheme": {"VM_PUBLIC_URL", "panel.example.com"},
		"public url ftp":       {"VM_PUBLIC_URL", "ftp://panel.example.com"},
		"unknown tz":           {"VM_TZ", "Mars/Olympus"},
		"proxies zero":         {"VM_TRUSTED_PROXIES", "0"},
		"proxies negative":     {"VM_TRUSTED_PROXIES", "-1"},
		"proxies garbage":      {"VM_TRUSTED_PROXIES", "yes-please"},
		"proxies bad cidr":     {"VM_TRUSTED_PROXIES", "10.0.0.0/64"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(kv[0], kv[1])
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q must be rejected", kv[0], kv[1])
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("empty means direct", func(t *testing.T) {
		for _, raw := range []string{"", "   "} {
			tp, err := ParseTrustedProxies(raw)
			if err != nil || !tp.Direct() {
				t.Fatalf("%q: tp=%+v err=%v", raw, tp, err)
			}
		}
	})

	t.Run("count", func(t *testing.T) {
		tp, err := ParseTrustedProxies(" 2 ")
		if err != nil || tp.Count != 2 || len(tp.CIDRs) != 0 || tp.Direct() {
			t.Fatalf("tp=%+v err=%v", tp, err)
		}
	})

	t.Run("cidrs", func(t *testing.T) {
		tp, err := ParseTrustedProxies("10.0.0.0/8, 172.18.0.0/16 ,")
		if err != nil {
			t.Fatal(err)
		}
		if tp.Count != 0 || len(tp.CIDRs) != 2 || tp.CIDRs[0] != "10.0.0.0/8" || tp.CIDRs[1] != "172.18.0.0/16" {
			t.Fatalf("tp=%+v", tp)
		}
		if tp.Direct() {
			t.Fatal("cidr config must not report Direct")
		}
	})

	t.Run("ipv6 cidr", func(t *testing.T) {
		if _, err := ParseTrustedProxies("2400:cb00::/32"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bare ip is not a cidr", func(t *testing.T) {
		if _, err := ParseTrustedProxies("10.0.0.1"); err == nil {
			t.Fatal("a bare IP must be rejected: CIDR is required")
		}
	})
}

func TestEnsureJWTSecret(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfg := Config{DataDir: dir}

	first, err := EnsureJWTSecret(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 { // 32 字节 hex
		t.Fatalf("secret len=%d want 64: %q", len(first), first)
	}
	if len(first) < 32 {
		t.Fatal("secret shorter than auth.MinSecretLen")
	}

	// 第二次必须读回同一个值，否则重启会把所有登录态踢掉
	second, err := EnsureJWTSecret(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("secret changed across calls: %q -> %q", first, second)
	}

	path := filepath.Join(dir, JWTSecretFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != first {
		t.Fatalf("file content %q does not match returned secret", raw)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil {
			t.Fatal(err)
		} else if perm := st.Mode().Perm(); perm != 0o600 {
			t.Fatalf("jwt.secret perm=%04o want 0600", perm)
		}
	}
}

func TestEnsureJWTSecretEnvWins(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	cfg := Config{DataDir: dir, JWTSecret: "from-env"}

	got, err := EnsureJWTSecret(cfg)
	if err != nil || got != "from-env" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, JWTSecretFile)); !os.IsNotExist(err) {
		t.Fatal("must not write jwt.secret when VM_JWT_SECRET is set")
	}
}

func TestEnsureJWTSecretRejectsEmptyFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, JWTSecretFile), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureJWTSecret(Config{DataDir: dir}); err == nil {
		t.Fatal("empty jwt.secret must be an error, not a silent empty key")
	}
}

func TestEnsureJWTSecretCreatesDataDirPrivately(t *testing.T) {
	clearEnv(t)
	dir := filepath.Join(t.TempDir(), "nested", "data")

	if _, err := EnsureJWTSecret(Config{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatal("data dir not created")
	}
	if runtime.GOOS != "windows" {
		if perm := st.Mode().Perm(); perm != DataDirPerm {
			t.Fatalf("data dir perm=%04o want %04o (holds password hashes and keys)", perm, DataDirPerm)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	for _, s := range []string{"debug", "info", "warn", "error", "INFO", " warn "} {
		if _, err := ParseLogLevel(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"", "verbose", "trace"} {
		if _, err := ParseLogLevel(s); err == nil {
			t.Errorf("%q must be rejected", s)
		}
	}
}
