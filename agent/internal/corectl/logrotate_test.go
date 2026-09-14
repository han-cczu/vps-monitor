package corectl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogrotateConfigWritten(t *testing.T) {
	m := &Manager{paths: Paths{Logrotate: filepath.Join(t.TempDir(), "logrotate", "sing-box")}}
	if err := m.installLogrotate(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.paths.Logrotate)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"daily", "rotate 7", "copytruncate", "compress"} {
		if !strings.Contains(string(raw), s) {
			t.Fatal(s)
		}
	}
}

func TestFallbackTruncatesOnlyOversizedRegularLog(t *testing.T) {
	if _, err := exec.LookPath("logrotate"); err == nil {
		t.Skip("system has logrotate")
	}
	path := filepath.Join(t.TempDir(), "box.log")
	if err := os.WriteFile(path, []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{paths: Paths{Log: path}}
	if err := m.maintainLog(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() != 5 {
		t.Fatal("small log changed")
	}
	if err := os.Truncate(path, rotateLogBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := m.maintainLog(); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	if info.Size() != 0 {
		t.Fatal("oversized log retained")
	}
	m.paths.Log = filepath.Dir(path)
	if err := m.maintainLog(); err == nil {
		t.Fatal("directory accepted")
	}
}
