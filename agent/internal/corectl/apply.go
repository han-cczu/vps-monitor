package corectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"vpsmon/proto"
)

type applyJournal struct {
	Previous  record `json:"previous"`
	HadConfig bool   `json:"had_config"`
}

func (m *Manager) healthy(ctx context.Context, ports []string) error {
	ctx, cancel := context.WithTimeout(ctx, m.healthTimeout)
	defer cancel()
	ticker := time.NewTicker(m.healthInterval)
	defer ticker.Stop()
	stable := 0
	var last error
	for {
		running, err := m.active(ctx)
		if err == nil && running {
			var p []string
			p, err = m.listening()
			if err == nil {
				if missing := Missing(ports, p); len(missing) > 0 {
					err = fmt.Errorf("missing listening ports: %s", strings.Join(missing, ","))
				}
			}
		} else if err == nil {
			err = fmt.Errorf("sing-box is not active")
		}
		if err == nil {
			stable++
			if stable >= 3 {
				return nil
			}
		} else {
			stable = 0
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("core health check failed: %w", errors.Join(last, ctx.Err()))
		case <-ticker.C:
		}
	}
}

func (m *Manager) apply(ctx context.Context, a proto.CoreApply) error {
	r := m.snapshot()
	if a.Revision < r.AppliedRevision || (a.Revision == r.AppliedRevision && a.ConfigSHA256 != r.ConfigSHA256) {
		return fmt.Errorf("stale or conflicting revision")
	}
	v, err := m.binaryVersion(ctx, m.paths.Binary)
	if err != nil {
		return err
	}
	if v != a.Version {
		return fmt.Errorf("installed version differs from requested version; install explicitly first")
	}
	compact, _, _ := CompactConfig(a.Config)
	if a.Revision == r.AppliedRevision && a.ConfigSHA256 == r.ConfigSHA256 {
		actual, readErr := os.ReadFile(m.paths.Config)
		_, sha, hashErr := CompactConfig(actual)
		if readErr == nil && hashErr == nil && sha == a.ConfigSHA256 {
			if err = m.healthy(ctx, a.Ports); err == nil {
				return nil
			}
		}
	}
	next := m.paths.Config + ".new"
	if err = atomicWrite(next, compact, 0600); err != nil {
		return err
	}
	defer os.Remove(next)
	// Do not forward checker output: it may contain credentials from the supplied JSON.
	if _, err = m.command(ctx, m.paths.Binary, "check", "-c", next); err != nil {
		return fmt.Errorf("sing-box check failed (configuration preserved): %w", err)
	}
	old, err := os.ReadFile(m.paths.Config)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	j := applyJournal{Previous: r, HadConfig: err == nil}
	if j.HadConfig {
		if err = atomicWrite(m.paths.backup(), old, 0600); err != nil {
			return err
		}
	}
	if err = writeJSON(m.paths.journal(), j); err != nil {
		return err
	}
	fail := func(cause error) error {
		if rollbackErr := m.rollback(context.WithoutCancel(ctx), j); rollbackErr != nil {
			return fmt.Errorf("apply failed: %w; rollback failed: %v", cause, rollbackErr)
		}
		return fmt.Errorf("apply failed, rolled back: %w", cause)
	}
	if err = os.Rename(next, m.paths.Config); err != nil {
		return fail(err)
	}
	fw, err := m.firewall(ctx, a.Ports)
	if err != nil {
		return fail(err)
	}
	if err = m.service(ctx, "restart"); err != nil {
		return fail(err)
	}
	if err = m.healthy(ctx, a.Ports); err != nil {
		return fail(err)
	}
	r.InstalledVersion = v
	r.AppliedRevision = a.Revision
	r.ConfigSHA256 = a.ConfigSHA256
	r.Ports = append([]string(nil), a.Ports...)
	r.Firewall = fw
	if err = m.save(r); err != nil {
		return fail(err)
	}
	if err = os.Remove(m.paths.journal()); err != nil {
		return fail(err)
	}
	return nil
}

func (m *Manager) rollback(ctx context.Context, j applyJournal) error {
	if j.HadConfig {
		b, err := os.ReadFile(m.paths.backup())
		if err != nil {
			return err
		}
		if err = atomicWrite(m.paths.Config, b, 0600); err != nil {
			return err
		}
		if err = m.service(ctx, "restart"); err != nil {
			return err
		}
		if err = m.healthy(ctx, j.Previous.Ports); err != nil {
			return err
		}
	} else {
		if err := m.service(ctx, "stop"); err != nil {
			return err
		}
		if err := os.Remove(m.paths.Config); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := m.save(j.Previous); err != nil {
		return err
	}
	return os.Remove(m.paths.journal())
}

// A process interrupted between replacement and commit must restore the last applied state.
func (m *Manager) recoverApply(ctx context.Context) error {
	b, err := os.ReadFile(m.paths.journal())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j applyJournal
	if err = json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("invalid core apply recovery record: %w", err)
	}
	if err = m.rollback(ctx, j); err != nil {
		return fmt.Errorf("recover interrupted apply: %w", err)
	}
	return nil
}
