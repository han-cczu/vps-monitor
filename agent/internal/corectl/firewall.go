package corectl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (m *Manager) firewall(ctx context.Context, ports []string) (string, error) {
	out, err := m.command(ctx, "ufw", "status")
	if err == nil && strings.Contains(out, "Status: active") {
		for _, p := range ports {
			if _, err = m.command(ctx, "ufw", "allow", p); err != nil {
				return "ufw", fmt.Errorf("ufw allow %s: %w", p, err)
			}
		}
		return "ufw", nil
	}
	if err != nil && !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
		return "unknown", fmt.Errorf("detect ufw: %w", err)
	}
	out, err = m.command(ctx, "firewall-cmd", "--state")
	if err == nil && strings.TrimSpace(out) == "running" {
		for _, p := range ports {
			if _, err = m.command(ctx, "firewall-cmd", "--permanent", "--add-port="+p); err != nil {
				return "firewalld", fmt.Errorf("firewalld allow %s: %w", p, err)
			}
		}
		if len(ports) > 0 {
			if _, err = m.command(ctx, "firewall-cmd", "--reload"); err != nil {
				return "firewalld", fmt.Errorf("firewalld reload: %w", err)
			}
		}
		return "firewalld", nil
	}
	if err != nil && !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, os.ErrNotExist) && !strings.Contains(out, "not running") {
		return "unknown", fmt.Errorf("detect firewalld: %w", err)
	}
	return "none", nil
}
