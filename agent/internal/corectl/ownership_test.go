package corectl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"vpsmon/proto"
)

func TestExternalCoreRefusesAllMutationPaths(t *testing.T) {
	m, f := fixture(t)
	old := seed(t, m)
	os.Remove(m.ownerPath())
	os.WriteFile(m.paths.Unit, []byte("external unit"), 0600)
	if err := writeJSON(m.paths.journal(), applyJournal{Previous: m.snapshot(), HadConfig: true}); err != nil {
		t.Fatal(err)
	}
	for _, job := range []any{applyMessage(t, 2, `{"replacement":true}`), proto.CoreAction{Type: proto.TypeCoreAction, Action: "restart"}, proto.CoreAction{Type: proto.TypeCoreAction, Action: "install", Version: "v1.14.0", SHA256: strings.Repeat("a", 64)}} {
		if _, err := m.Execute(context.Background(), job); !errors.Is(err, ErrExternalCore) {
			t.Fatalf("write not rejected: %v", err)
		}
		b, _ := json.Marshal(job)
		if err := m.Handle(b); err == nil {
			t.Fatal("remote write queued")
		}
	}
	if _, err := m.Stats(context.Background()); !errors.Is(err, ErrExternalCore) {
		t.Fatal("external reset stats accepted")
	}
	if err := m.Purge(context.Background()); !errors.Is(err, ErrExternalCore) {
		t.Fatal("external purge accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	<-m.Ready()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	b, _ := os.ReadFile(m.paths.Config)
	if string(b) != string(old.Config) {
		t.Fatal("external config changed")
	}
	if _, err := os.Stat(m.paths.journal()); err != nil {
		t.Fatal("external recovery record touched")
	}
	if len(f.calls) != 0 {
		t.Fatal("external command executed", f.calls)
	}
}
func TestOwnershipReplacementAndObserveMode(t *testing.T) {
	for _, target := range []string{"binary", "config", "unit", "dropin", "observe"} {
		t.Run(target, func(t *testing.T) {
			m, f := fixture(t)
			seed(t, m)
			switch target {
			case "binary":
				os.WriteFile(m.paths.Binary, []byte("external"), 0755)
			case "config":
				os.WriteFile(m.paths.Config, []byte(`{"external":true}`), 0600)
			case "unit":
				os.WriteFile(m.paths.Unit, []byte("external"), 0600)
			case "dropin":
				os.Mkdir(m.paths.Unit+".d", 0700)
				os.WriteFile(m.paths.Unit+".d/override.conf", []byte("external"), 0600)
			case "observe":
				m.observeOnly = true
			}
			if _, err := m.Execute(context.Background(), proto.CoreAction{Type: proto.TypeCoreAction, Action: "restart"}); !errors.Is(err, ErrExternalCore) {
				t.Fatal("changed ownership accepted", err)
			}
			if len(f.calls) != 0 {
				t.Fatal("service command after replacement")
			}
		})
	}
}
func TestExternalLogMaintenanceDoesNotTruncate(t *testing.T) {
	m, _ := fixture(t)
	os.Remove(m.ownerPath())
	// fixture uses platform-native separators.
	f, err := os.CreateTemp(t.TempDir(), "external-log")
	if err != nil {
		t.Fatal(err)
	}
	m.paths.Log = f.Name()
	f.Truncate(rotateLogBytes + 1)
	f.Close()
	if err := m.maintainLog(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(m.paths.Log)
	if st.Size() != rotateLogBytes+1 {
		t.Fatal("external log truncated")
	}
}

func TestDamagedLocalStateDoesNotPreventObservationOrGrantOwnership(t *testing.T) {
	m, _ := fixture(t)
	if err := os.WriteFile(m.paths.State, []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	observerSafe, err := New(Options{Paths: m.paths, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal("damaged core state prevented Agent startup", err)
	}
	defer observerSafe.Close()
	if observerSafe.Management() == "managed" || observerSafe.installPermission() == nil {
		t.Fatal("damaged local state granted write access")
	}
}
