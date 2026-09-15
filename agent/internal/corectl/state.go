package corectl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vpsmon/proto"
)

type record struct {
	InstalledVersion string   `json:"installed_version"`
	AppliedRevision  int64    `json:"applied_revision"`
	ConfigSHA256     string   `json:"config_sha256"`
	Ports            []string `json:"ports"`
	Firewall         string   `json:"firewall"`
}

func readRecord(path string) (record, error) {
	var r record
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("invalid core state file: %w", err)
	}
	if r.AppliedRevision < 0 || (r.AppliedRevision > 0 && !validSHA(r.ConfigSHA256)) || (r.InstalledVersion != "" && !versionRE.MatchString(r.InstalledVersion)) {
		return r, fmt.Errorf("invalid persisted core state")
	}
	return r, nil
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".core-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWrite(path, b, 0600)
}
func (m *Manager) save(r record) error {
	if err := writeJSON(m.paths.State, r); err != nil {
		return fmt.Errorf("persist core state: %w", err)
	}
	m.mu.Lock()
	m.record = r
	m.mu.Unlock()
	return nil
}
func (m *Manager) snapshot() record {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.record
	r.Ports = append([]string(nil), r.Ports...)
	return r
}
func (m *Manager) AppliedRevision() int64 {
	r, err := m.reportedRecord()
	if err != nil {
		return m.snapshot().AppliedRevision
	}
	return r.AppliedRevision
}

func (m *Manager) reportedRecord() (record, error) {
	r, err := readRecord(m.paths.State)
	if err != nil {
		return r, err
	}
	// Until the recovery journal is removed, the new revision has not committed.
	b, err := os.ReadFile(m.paths.journal())
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	var j applyJournal
	if err = json.Unmarshal(b, &j); err != nil {
		return r, fmt.Errorf("invalid apply recovery record: %w", err)
	}
	return j.Previous, nil
}

func parseVersion(out string) (string, error) {
	line, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "sing-box" || f[1] != "version" {
		return "", fmt.Errorf("invalid sing-box version output")
	}
	v := "v" + strings.TrimPrefix(f[2], "v")
	if !versionRE.MatchString(v) {
		return "", fmt.Errorf("invalid sing-box version")
	}
	return v, nil
}
func (m *Manager) binaryVersion(ctx context.Context, path string) (string, error) {
	out, err := m.command(ctx, path, "version")
	if err != nil {
		return "", fmt.Errorf("execute sing-box version: %w", err)
	}
	return parseVersion(out)
}
func (m *Manager) State(ctx context.Context) proto.CoreState {
	if m.verifyOwned() != nil {
		st := proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", Listening: []string{}, Firewall: "none"}
		m.mu.Lock()
		m.lastState = st
		m.mu.Unlock()
		return st
	}
	r := m.snapshot()
	stored, readErr := m.reportedRecord()
	if readErr == nil {
		r = stored
	}
	st := proto.CoreState{Type: proto.TypeCoreState, Core: "sing-box", InstalledVersion: r.InstalledVersion, AppliedRevision: r.AppliedRevision, ConfigSHA256: r.ConfigSHA256, Listening: []string{}, Firewall: r.Firewall}
	if st.Firewall == "" {
		st.Firewall = "none"
	}
	m.mu.Lock()
	msg := m.lastError
	statsErr := m.statsError
	m.mu.Unlock()
	if msg == "" {
		msg = statsErr
	}
	if readErr != nil {
		msg = readErr.Error()
	}
	v, err := m.observedVersion(ctx)
	st.InstalledVersion = v
	if err != nil {
		msg = err.Error()
	}
	running, err := m.active(ctx)
	st.Running = running
	if err != nil {
		msg = err.Error()
	}
	ports, err := m.listening()
	if err == nil {
		st.Listening = ports
	} else if msg == "" {
		msg = err.Error()
	}
	if msg != "" {
		msg = boundedError(msg)
		st.Error = &msg
	}
	m.mu.Lock()
	m.lastState = st
	m.mu.Unlock()
	return st
}

func (m *Manager) observedVersion(ctx context.Context) (string, error) {
	m.versionMu.Lock()
	defer m.versionMu.Unlock()
	st, err := os.Stat(m.paths.Binary)
	if os.IsNotExist(err) {
		m.versionStamp = ""
		m.versionCache = ""
		return "", nil
	}
	if err != nil {
		return "", err
	}
	stamp := fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	if stamp == m.versionStamp {
		return m.versionCache, nil
	}
	v, err := m.binaryVersion(ctx, m.paths.Binary)
	if err != nil {
		return "", err
	}
	m.versionStamp = stamp
	m.versionCache = v
	return v, nil
}
func boundedError(s string) string {
	if len(s) > 2048 {
		return strings.ToValidUTF8(s[:2048], "")
	}
	return s
}
func (m *Manager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = ""
	if err != nil {
		m.lastError = boundedError(err.Error())
	}
}
func (m *Manager) emitState(ctx context.Context, reqID string, err error) proto.CoreState {
	// A cancelled apply may already have restored the service. Observe cleanup
	// with its own short budget rather than reporting a false stopped state.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	st := m.State(ctx)
	st.ReqID = reqID
	if err != nil {
		s := boundedError(err.Error())
		st.Error = &s
	}
	if m.send != nil {
		_ = m.send(st)
	}
	return st
}
