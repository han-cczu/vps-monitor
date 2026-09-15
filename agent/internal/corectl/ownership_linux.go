//go:build linux

package corectl

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (m *Manager) managedPlatform() bool {
	if m.paths.Unit != DefaultPaths().Unit {
		return true
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

func (m *Manager) verifyRuntime() error {
	if m.paths.Unit != DefaultPaths().Unit {
		return nil
	}
	for _, dir := range []string{"/run/systemd/system/sing-box.service.d", "/usr/lib/systemd/system/sing-box.service.d", "/lib/systemd/system/sing-box.service.d"} {
		entries, e := os.ReadDir(dir)
		if e == nil && len(entries) > 0 || e != nil && !os.IsNotExist(e) {
			return ErrExternalCore
		}
	}
	if _, e := os.Lstat("/run/systemd/system/sing-box.service"); !os.IsNotExist(e) {
		return ErrExternalCore
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ErrExternalCore
	}
	for _, entry := range entries {
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		group, e := os.ReadFile(filepath.Join(dir, "cgroup"))
		if e != nil {
			continue
		}
		belongs := false
		for _, part := range strings.Split(strings.TrimSpace(string(group)), "/") {
			if part == "sing-box.service" {
				belongs = true
			}
		}
		if !belongs {
			continue
		}
		exe, e := os.Readlink(filepath.Join(dir, "exe"))
		if e != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		name := filepath.Base(exe)
		if name != "sing-box" && !strings.HasPrefix(name, "xray") {
			continue
		}
		if exe != m.paths.Binary {
			return ErrExternalCore
		}
		args, e := os.ReadFile(filepath.Join(dir, "cmdline"))
		if e != nil || len(args) > 65536 {
			return ErrExternalCore
		}
		parts := strings.Split(strings.TrimRight(string(args), "\x00"), "\x00")
		if len(parts) != 4 || parts[1] != "run" || parts[2] != "-c" || parts[3] != m.paths.Config {
			return ErrExternalCore
		}
	}
	return nil
}
func openLogForTruncate(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
