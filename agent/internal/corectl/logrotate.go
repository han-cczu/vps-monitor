package corectl

import (
	"fmt"
	"os"
	"os/exec"
)

const rotateLogBytes = 50 << 20

// LogrotateConfig is static: message content never becomes a filesystem path or shell command.
func LogrotateConfig() string {
	return `/var/log/sing-box/box.log {
 daily
 rotate 7
 compress
 delaycompress
 missingok
 notifempty
 copytruncate
 su root root
}
`
}
func (m *Manager) installLogrotate() error {
	if m.paths.Logrotate == "" {
		return nil
	}
	return atomicWrite(m.paths.Logrotate, []byte(LogrotateConfig()), 0644)
}
func (m *Manager) maintainLog() error {
	if _, err := exec.LookPath("logrotate"); err == nil {
		return nil
	}
	info, err := os.Lstat(m.paths.Log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to truncate non-regular sing-box log")
	}
	if info.Size() <= rotateLogBytes {
		return nil
	}
	return os.Truncate(m.paths.Log, 0)
}
