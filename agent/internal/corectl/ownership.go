package corectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"vpsmon/proto"
)

var ErrExternalCore = errors.New("代理资源未通过本项目所有权核验，已禁用管理操作")

// Allowed digests include the previous and intended resources during a local
// transaction. Recovery may touch only one of those exact resource identities.
type ownershipRecord struct {
	Schema    int                 `json:"schema"`
	Resources map[string][]string `json:"resources"`
	Parents   map[string]string   `json:"parents"`
}

func (m *Manager) ownerPath() string {
	return filepath.Join(filepath.Dir(m.paths.State), "core-owner.json")
}
func (m *Manager) resourcePaths() []string {
	paths := []string{m.paths.Binary, m.paths.Unit, m.paths.Config}
	if m.paths.Logrotate != "" {
		paths = append(paths, m.paths.Logrotate)
	}
	return paths
}

func resourceDigest(path string) (string, error) {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxBinarySize {
		return "", ErrExternalCore
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrExternalCore
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return "", ErrExternalCore
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxBinarySize+1)); err != nil {
		return "", ErrExternalCore
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(st, after) || st.Size() != after.Size() || st.ModTime() != after.ModTime() {
		return "", ErrExternalCore
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parentIdentity(path string) (string, error) {
	// Resolve the nearest existing ancestor, even before an initial installation.
	original := filepath.Dir(path)
	dir := original
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			suffix, _ := filepath.Rel(dir, original)
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(dir) == dir {
			return "", ErrExternalCore
		}
		dir = filepath.Dir(dir)
	}
}

func (m *Manager) readOwner() (ownershipRecord, error) {
	var owner ownershipRecord
	st, err := os.Lstat(m.ownerPath())
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16<<10 {
		return owner, ErrExternalCore
	}
	b, err := os.ReadFile(m.ownerPath())
	if err != nil || json.Unmarshal(b, &owner) != nil || owner.Schema != 1 {
		return owner, ErrExternalCore
	}
	return owner, nil
}

func (m *Manager) verifyOwned() error {
	if m.observeOnly || m.invalidOwnership {
		return ErrExternalCore
	}
	o, err := m.readOwner()
	if err != nil {
		return err
	}
	for _, path := range m.resourcePaths() {
		hash, e := resourceDigest(path)
		if e != nil || !slices.Contains(o.Resources[path], hash) {
			return ErrExternalCore
		}
		parent, e := parentIdentity(path)
		if e != nil || o.Parents[path] != parent {
			return ErrExternalCore
		}
	}
	if m.paths.Log != "" {
		parent, e := parentIdentity(m.paths.Log)
		if e != nil || o.Parents[m.paths.Log] != parent {
			return ErrExternalCore
		}
	}
	// A drop-in can redirect an otherwise unchanged unit to an external core.
	if entries, e := os.ReadDir(m.paths.Unit + ".d"); e == nil && len(entries) > 0 || e != nil && !os.IsNotExist(e) {
		return ErrExternalCore
	}
	return m.verifyRuntime()
}

func (m *Manager) createOwner() error {
	o := ownershipRecord{Schema: 1, Resources: map[string][]string{}, Parents: map[string]string{}}
	for _, path := range m.resourcePaths() {
		hash, e := resourceDigest(path)
		if e != nil {
			return e
		}
		parent, e := parentIdentity(path)
		if e != nil {
			return e
		}
		o.Resources[path] = []string{hash}
		o.Parents[path] = parent
	}
	if m.paths.Log != "" {
		parent, e := parentIdentity(m.paths.Log)
		if e != nil {
			return e
		}
		o.Parents[m.paths.Log] = parent
	}
	return writeJSON(m.ownerPath(), o)
}

func (m *Manager) sealOwner() error {
	if err := m.verifyOwned(); err != nil {
		return err
	}
	return m.createOwner()
}

func (m *Manager) claimEmptyOwner() error {
	o := ownershipRecord{Schema: 1, Resources: map[string][]string{}, Parents: map[string]string{}}
	for _, p := range m.resourcePaths() {
		hash, err := resourceDigest(p)
		if err != nil || hash != "" {
			return ErrExternalCore
		}
		parent, err := parentIdentity(p)
		if err != nil {
			return err
		}
		o.Resources[p] = []string{""}
		o.Parents[p] = parent
	}
	if m.paths.Log != "" {
		parent, err := parentIdentity(m.paths.Log)
		if err != nil {
			return err
		}
		o.Parents[m.paths.Log] = parent
	}
	return writeJSON(m.ownerPath(), o)
}

// migrateOwner requires matching legacy installation, unit and applied-config
// evidence. A same-named service or a legacy core.json alone never grants rights.
func (m *Manager) migrateOwner(ctx context.Context) error {
	if m.observeOnly || m.invalidOwnership {
		return ErrExternalCore
	}
	if _, err := os.Lstat(m.ownerPath()); !os.IsNotExist(err) {
		return m.verifyOwned()
	}
	r := m.snapshot()
	u, err := os.ReadFile(m.paths.Unit)
	if err != nil || string(u) != ServiceUnit() || r.InstalledVersion == "" {
		return ErrExternalCore
	}
	if _, err := os.Lstat(m.paths.journal()); !os.IsNotExist(err) {
		return ErrExternalCore
	}
	hash, err := resourceDigest(m.paths.Config)
	if err != nil || (r.AppliedRevision > 0 && hash != r.ConfigSHA256) || (r.AppliedRevision == 0 && hash != "") {
		return ErrExternalCore
	}
	v, err := m.binaryVersion(ctx, m.paths.Binary)
	if err != nil || v != r.InstalledVersion {
		return ErrExternalCore
	}
	return m.createOwner()
}

func (m *Manager) installPermission() error {
	if m.observeOnly || m.invalidOwnership {
		return ErrExternalCore
	}
	if !m.managedPlatform() {
		return ErrExternalCore
	}
	if _, err := os.Lstat(m.ownerPath()); err == nil {
		return m.verifyOwned()
	} else if !os.IsNotExist(err) {
		return ErrExternalCore
	}
	for _, path := range append(m.resourcePaths(), m.paths.Log, m.paths.Logrotate, m.paths.backup(), m.paths.journal()) {
		if path == "" {
			continue
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return ErrExternalCore
		}
	}
	if m.externalPresent != nil && m.externalPresent() {
		return ErrExternalCore
	}
	return nil
}

func (m *Manager) allowResource(path, digest string) error {
	if err := m.verifyOwned(); err != nil {
		return err
	}
	o, err := m.readOwner()
	if err != nil {
		return err
	}
	if !slices.Contains(o.Resources[path], digest) {
		o.Resources[path] = append(o.Resources[path], digest)
	}
	return writeJSON(m.ownerPath(), o)
}

func (m *Manager) Management() string {
	if m.verifyOwned() == nil {
		return "managed"
	}
	if m.installPermission() == nil {
		return "none"
	}
	if (!m.managedPlatform() || m.observeOnly) && (m.externalPresent == nil || !m.externalPresent()) {
		return "unknown"
	}
	return "external"
}

func (m *Manager) OwnsInstance(binary, service string, configs []string) bool {
	return binary == m.paths.Binary && strings.TrimSuffix(service, ".service") == "sing-box" && len(configs) == 1 && configs[0] == m.paths.Config && m.verifyOwned() == nil
}

func (m *Manager) OwnedLogs(lines int) (string, error) {
	if err := m.verifyOwned(); err != nil {
		return "", err
	}
	return Tail(m.paths.Log, lines)
}

// Purge removes exact owned resources only. It never recursively deletes a
// same-named third-party installation directory.
func (m *Manager) Purge(ctx context.Context) error {
	return m.withLock(ctx, func() error {
		if err := m.verifyOwned(); err != nil {
			return err
		}
		if err := m.service(ctx, "stop"); err != nil {
			return err
		}
		if err := m.service(ctx, "disable"); err != nil {
			return err
		}
		if err := m.verifyOwned(); err != nil {
			return err
		}
		for _, p := range m.resourcePaths() {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		// Preserve logs/backups and the Agent's own state for diagnosis.
		return os.Remove(m.ownerPath())
	})
}

func (m *Manager) guardJob(job any) error {
	if a, ok := job.(proto.CoreAction); ok && a.Action == "install" {
		return m.installPermission()
	}
	return m.verifyOwned()
}
