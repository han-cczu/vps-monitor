package corectl

import (
	"crypto/sha256"
	"encoding/hex"
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
	h := sha256.Sum256([]byte(LogrotateConfig()))
	if err := m.allowResource(m.paths.Logrotate, hex.EncodeToString(h[:])); err != nil {
		return err
	}
	return atomicWrite(m.paths.Logrotate, []byte(LogrotateConfig()), 0644)
}
func (m *Manager) maintainLog() error {
	if m.verifyOwned() != nil {
		return nil
	}
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
	f, err := openLogForTruncate(m.paths.Log)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return ErrExternalCore
	}
	if err := m.verifyOwned(); err != nil {
		return err
	}
	return f.Truncate(0)
}
