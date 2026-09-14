// Package corectl manages a single sing-box service through a fixed command allowlist.
package corectl

import "path/filepath"

type Paths struct{ Binary, Config, Log, State, Unit, Proc, Logrotate string }

func DefaultPaths() Paths {
	return Paths{Binary: "/usr/local/bin/sing-box", Config: "/etc/sing-box/config.json",
		Log: "/var/log/sing-box/box.log", State: "/var/lib/vps-agent/core.json",
		Unit: "/etc/systemd/system/sing-box.service", Proc: "/proc/net", Logrotate: "/etc/logrotate.d/sing-box"}
}

func (p Paths) backup() string  { return p.Config + ".bak" }
func (p Paths) journal() string { return filepath.Join(filepath.Dir(p.State), "core-apply.json") }
