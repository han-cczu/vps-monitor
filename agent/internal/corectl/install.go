package corectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"vpsmon/proto"
)

const maxBinarySize = 64 << 20

func downloadURL(server, version, arch string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid panel URL")
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("panel URL must use ws or wss")
	}
	u.Path = "/api/agent/corefiles/" + version + "/" + arch
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (m *Manager) install(ctx context.Context, a proto.CoreAction) error {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("unsupported architecture")
	}
	address, err := downloadURL(m.server, a.Version, runtime.GOARCH)
	if err != nil {
		return err
	}
	if m.token == "" {
		return fmt.Errorf("agent token required for core download")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	// Redirects could send the node credential or executable download to another origin.
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("core download redirects are disabled") }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download core: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("core download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBinarySize {
		return fmt.Errorf("core binary exceeds 64 MiB")
	}
	if err = os.MkdirAll(filepath.Dir(m.paths.Binary), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.paths.Binary), ".sing-box-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinarySize+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 || n > maxBinarySize || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("core binary size or SHA256 mismatch")
	}
	if err = os.Chmod(tmp, 0755); err != nil {
		return err
	}
	v, err := m.binaryVersion(ctx, tmp)
	if err != nil {
		return err
	}
	if v != a.Version {
		return fmt.Errorf("downloaded binary version does not match requested version")
	}
	if err = os.MkdirAll(filepath.Dir(m.paths.Log), 0700); err != nil {
		return err
	}
	old, readErr := os.ReadFile(m.paths.Binary)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	oldUnit, unitErr := os.ReadFile(m.paths.Unit)
	if unitErr != nil && !os.IsNotExist(unitErr) {
		return unitErr
	}
	unit := []byte(ServiceUnit())
	changed := string(oldUnit) != string(unit)
	if changed {
		if err = atomicWrite(m.paths.Unit, unit, 0644); err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		var restore error
		if readErr == nil {
			restore = atomicWrite(m.paths.Binary, old, 0755)
		} else {
			restore = os.Remove(m.paths.Binary)
			if os.IsNotExist(restore) {
				restore = nil
			}
		}
		if changed {
			var e error
			if unitErr == nil {
				e = atomicWrite(m.paths.Unit, oldUnit, 0644)
			} else {
				e = os.Remove(m.paths.Unit)
			}
			if e != nil {
				return fmt.Errorf("%w; restore unit: %v", cause, e)
			}
			_, _ = m.command(context.WithoutCancel(ctx), "systemctl", "daemon-reload")
		}
		if restore != nil {
			return fmt.Errorf("%w; restore binary: %v", cause, restore)
		}
		return cause
	}
	if err = os.Rename(tmp, m.paths.Binary); err != nil {
		return rollback(err)
	}
	if changed {
		if _, err = m.command(ctx, "systemctl", "daemon-reload"); err != nil {
			return rollback(fmt.Errorf("systemctl daemon-reload: %w", err))
		}
	}
	if err = m.service(ctx, "enable"); err != nil {
		return rollback(err)
	}
	r := m.snapshot()
	r.InstalledVersion = v
	if err = m.save(r); err != nil {
		return rollback(err)
	}
	return nil
}
