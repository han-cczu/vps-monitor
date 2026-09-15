package corectl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const commandTimeout = 30 * time.Second

// Runner allows deterministic failure injection without a shell or a real service.
type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}
type execRunner struct{}
type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 8192 {
		_, _ = b.Buffer.Write(p[:min(len(p), 8192-b.Len())])
	}
	return n, nil
}
func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second // Bound inherited output pipes after a command exits/is killed.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var b limitedOutput
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := cmd.Run()
	if ctx.Err() != nil {
		return b.String(), ctx.Err()
	}
	return b.String(), err
}

func (m *Manager) command(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return m.runner.Run(ctx, name, args...)
}
func (m *Manager) service(ctx context.Context, action string) error {
	if err := m.verifyOwned(); err != nil {
		return err
	}
	switch action {
	case "start", "stop", "restart", "enable", "disable":
	default:
		return fmt.Errorf("unsupported systemctl action")
	}
	// A failed configuration can exhaust systemd's start limit. Clear it before
	// an explicit start/restart so even an immediate rollback can recover.
	if action == "start" || action == "restart" {
		if _, err := m.command(ctx, "systemctl", "reset-failed", "sing-box.service"); err != nil {
			return fmt.Errorf("systemctl reset-failed sing-box.service: %w", err)
		}
	}
	_, err := m.command(ctx, "systemctl", action, "sing-box.service")
	if err != nil {
		return fmt.Errorf("systemctl %s sing-box.service: %w", action, err)
	}
	return nil
}
func (m *Manager) active(ctx context.Context) (bool, error) {
	out, err := m.command(ctx, "systemctl", "is-active", "sing-box.service")
	if err == nil {
		return strings.TrimSpace(out) == "active", nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 3 || exit.ExitCode() == 4) {
		return false, nil
	}
	return false, fmt.Errorf("systemctl is-active sing-box.service: %w", err)
}
func ServiceUnit() string {
	return `[Unit]
Description=sing-box (managed by VPS Monitor)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/sing-box run -c /etc/sing-box/config.json
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
UMask=0077
StandardOutput=append:/var/log/sing-box/box.log
StandardError=append:/var/log/sing-box/box.log

[Install]
WantedBy=multi-user.target
`
}
