package corectl

import (
	"os"
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
