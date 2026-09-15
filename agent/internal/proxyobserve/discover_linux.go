//go:build linux

package proxyobserve

import (
	"context"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"vpsmon/proto"
)

type candidate struct {
	core, binary, exe, service, namespace, start, root, database string
	configs                                                      []string
	pid, parent, manager                                         int
	managerRunning                                               *bool
	issues                                                       []string
}

var errPortsLimit = errors.New("ports_limit")

func coreName(name string) string {
	if name == "sing-box" {
		return "sing-box"
	}
	if name == "xray" || strings.HasPrefix(name, "xray-linux-") {
		return "xray"
	}
	return ""
}
func ExternalPresent() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, p := range entries {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", p.Name(), "exe"))
		if err == nil && coreName(filepath.Base(strings.TrimSuffix(exe, " (deleted)"))) != "" {
			return true
		}
	}
	for _, p := range []string{"/etc/s-box/sing-box", "/usr/local/x-ui/x-ui", "/usr/local/x-ui/bin/xray-linux-amd64", "/usr/local/x-ui/bin/xray-linux-arm64", "/usr/local/bin/xray", "/etc/init.d/sing-box", "/usr/lib/systemd/system/sing-box.service", "/lib/systemd/system/sing-box.service"} {
		if _, err := os.Lstat(p); err == nil || !os.IsNotExist(err) {
			return true
		}
	}
	return false
}
func processInfo(proc string, pid int) (candidate, []string, error) {
	dir := filepath.Join(proc, strconv.Itoa(pid))
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil {
		return candidate{}, nil, err
	}
	deleted := strings.HasSuffix(exe, " (deleted)")
	exe = strings.TrimSuffix(exe, " (deleted)")
	c := candidate{pid: pid, binary: exe, exe: filepath.Join(dir, "exe"), core: coreName(filepath.Base(exe)), root: filepath.Join(dir, "root")}
	if deleted {
		c.issues = append(c.issues, "binary_replaced")
	}
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return c, nil, err
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return c, nil, errRead
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 20 {
		return c, nil, errRead
	}
	c.parent, _ = strconv.Atoi(fields[1])
	c.start = fields[19]
	if raw, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			base := filepath.Base(line)
			if strings.HasSuffix(base, ".service") {
				c.service = label(base, 128)
				break
			}
		}
	}
	c.namespace, _ = os.Readlink(filepath.Join(dir, "ns", "net"))
	args, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(args) > 64<<10 {
		return c, nil, errRead
	}
	return c, strings.Split(strings.TrimRight(string(args), "\x00"), "\x00"), nil
}
func configArguments(args []string, cwd string) ([]string, []string) {
	paths := []string{}
	dirs := []string{}
	base := cwd
	// Global working-directory options apply regardless of flag order.
	for i := 1; i < len(args); i++ {
		key, value, eq := strings.Cut(args[i], "=")
		if key != "-D" && key != "--directory" {
			continue
		}
		if !eq && i+1 < len(args) {
			i++
			value = args[i]
		}
		if value != "" {
			if !filepath.IsAbs(value) {
				value = filepath.Join(cwd, value)
			}
			base = filepath.Clean(value)
		}
	}
	for i := 1; i < len(args); i++ {
		key, value, equals := strings.Cut(args[i], "=")
		if !equals && (key == "-c" || key == "--config" || key == "-config" || key == "-confdir" || key == "-C" || key == "--config-directory" || key == "-D" || key == "--directory") && i+1 < len(args) {
			i++
			value = args[i]
		}
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		switch key {
		case "-c", "--config", "-config":
			paths = append(paths, filepath.Clean(value))
		case "-C", "-confdir", "--config-directory":
			dirs = append(dirs, filepath.Clean(value))
		case "-D", "--directory":
			// Already handled in the first pass.
		}
	}
	return paths, dirs
}
func (o *Observer) discover() ([]candidate, bool) {
	entries, err := os.ReadDir(o.options.Proc)
	if err != nil {
		return nil, false
	}
	complete := true
	candidates := []candidate{}
	managers := map[int]candidate{}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil {
			continue
		}
		exe, e := os.Readlink(filepath.Join(o.options.Proc, entry.Name(), "exe"))
		if e != nil {
			if errors.Is(e, os.ErrPermission) {
				complete = false
			}
			continue
		}
		name := filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
		boundCore := ""
		for _, b := range o.options.Config.Bindings {
			if b.Binary == strings.TrimSuffix(exe, " (deleted)") {
				boundCore = b.Core
				break
			}
		}
		if name != "x-ui" && coreName(name) == "" && boundCore == "" {
			continue
		}
		c, args, e := processInfo(o.options.Proc, pid)
		if e != nil {
			if !os.IsNotExist(e) {
				complete = false
			}
			continue
		}
		if name == "x-ui" {
			managers[pid] = c
			continue
		}
		if boundCore != "" {
			c.core = boundCore
		}
		cwd, _ := os.Readlink(filepath.Join(o.options.Proc, entry.Name(), "cwd"))
		var dirs []string
		c.configs, dirs = configArguments(args, cwd)
		for _, dir := range dirs {
			files, e := os.ReadDir(filepath.Join(c.root, dir))
			if e != nil {
				c.issues = append(c.issues, "config_directory_unreadable")
				continue
			}
			for _, file := range files {
				if !file.IsDir() && (strings.HasSuffix(file.Name(), ".json") || strings.HasSuffix(file.Name(), ".jsonc")) {
					c.configs = append(c.configs, filepath.Join(dir, file.Name()))
				}
			}
		}
		if len(c.configs) > 16 {
			c.configs = c.configs[:16]
			c.issues = append(c.issues, "config_files_limit")
		}
		candidates = append(candidates, c)
	}
	seen := map[string]bool{}
	for idx := range candidates {
		c := &candidates[idx]
		seen[c.binary] = true
		if manager, ok := managers[c.parent]; ok {
			c.manager = manager.pid
			alive := true
			c.managerRunning = &alive
			if c.service == "" {
				c.service = manager.service
			}
		}
		if c.service == "" && o.options.Proc == "/proc" {
			name, binary := "sing-box", c.binary
			if c.manager > 0 {
				name = "x-ui"
				binary = managers[c.parent].binary
			} else if c.core == "xray" {
				name = "xray"
			}
			if raw, e := readStable(filepath.Join(c.root, "/etc/init.d", name), 64<<10); e == nil && strings.Contains(string(raw), binary) {
				if _, e := os.Stat(filepath.Join(c.root, "/run/openrc/started", name)); e == nil {
					c.service = name + " (OpenRC)"
				}
			}
		}
		for _, b := range o.options.Config.Bindings {
			if c.binary == b.Binary {
				if len(b.ConfigPaths) > 0 {
					c.configs = b.ConfigPaths
				}
				c.database = b.Database
				if b.Service != "" {
					c.service = b.Service
				}
			}
		}
	}
	for _, manager := range managers {
		found := false
		for _, c := range candidates {
			if c.manager == manager.pid {
				found = true
			}
		}
		if !found {
			alive := true
			binary := filepath.Join(filepath.Dir(manager.binary), "bin", "xray-linux-amd64")
			if _, err := os.Stat(filepath.Join(manager.root, binary)); err != nil {
				binary = filepath.Join(filepath.Dir(manager.binary), "bin", "xray-linux-arm64")
			}
			candidates = append(candidates, candidate{core: "xray", binary: binary, root: manager.root, configs: []string{filepath.Join(filepath.Dir(binary), "config.json")}, service: manager.service, manager: manager.pid, managerRunning: &alive, namespace: manager.namespace})
			seen[binary] = true
		}
	}
	bindings := append([]Binding{}, o.options.Config.Bindings...)
	bindings = append(bindings, Binding{Core: "sing-box", Binary: "/etc/s-box/sing-box", Service: "sing-box", ConfigPaths: []string{"/etc/s-box/sb.json"}}, Binding{Core: "sing-box", Binary: "/usr/local/bin/sing-box", Service: "sing-box", ConfigPaths: []string{"/etc/sing-box/config.json"}}, Binding{Core: "xray", Binary: "/usr/local/bin/xray", ConfigPaths: []string{"/usr/local/etc/xray/config.json"}})
	for _, arch := range []string{"amd64", "arm64"} {
		bindings = append(bindings, Binding{Core: "xray", Binary: "/usr/local/x-ui/bin/xray-linux-" + arch, Service: "x-ui", ConfigPaths: []string{"/usr/local/x-ui/bin/config.json"}})
	}
	if o.options.Proc == "/proc" {
		for _, b := range bindings {
			if seen[b.Binary] {
				continue
			}
			if st, err := os.Stat(b.Binary); err == nil && st.Mode().IsRegular() {
				stopped := candidate{core: b.Core, binary: b.Binary, root: "/", configs: b.ConfigPaths, service: b.Service, database: b.Database}
				if b.Service == "x-ui" {
					running := false
					stopped.managerRunning = &running
				}
				candidates = append(candidates, stopped)
				seen[b.Binary] = true
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].binary == candidates[j].binary {
			return candidates[i].pid < candidates[j].pid
		}
		return candidates[i].binary < candidates[j].binary
	})
	if len(candidates) > proto.ProxyMaxInstances {
		candidates = candidates[:proto.ProxyMaxInstances]
		complete = false
	}
	return candidates, complete
}

var versionLine = regexp.MustCompile(`(?i)^(?:sing-box version|xray)\s+v?(\d+\.\d+(?:\.\d+)?(?:[-+.][a-zA-Z0-9.]+)?)`)

func version(ctx context.Context, c candidate) string {
	// Never execute a stopped candidate, arbitrary script or binding. A running
	// candidate must also identify as the expected Go core module.
	if c.pid == 0 || c.exe == "" {
		return ""
	}
	file, err := os.Open(c.exe)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := buildinfo.Read(file)
	if err != nil {
		return ""
	}
	expected := "github.com/sagernet/sing-box"
	if c.core == "xray" {
		expected = "github.com/xtls/xray-core"
	}
	if !strings.HasPrefix(strings.ToLower(info.Path), expected) && !strings.HasPrefix(strings.ToLower(info.Main.Path), expected) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "version")
	cmd.ExtraFiles = []*os.File{file}
	cmd.WaitDelay = time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	if cmd.Run() != nil {
		return ""
	}
	m := versionLine.FindStringSubmatch(out.String())
	if len(m) < 2 {
		return ""
	}
	return "v" + m[1]
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p[:min(n, 4096-len(b.data))]...)
	return n, nil
}
func (b *boundedOutput) String() string { return string(b.data) }

func (o *Observer) Scan(ctx context.Context) ([]proto.ObservedInstance, bool) {
	candidates, complete := o.discover()
	current := map[string]proto.ObservedInstance{}
	budget := 4096
	for _, c := range candidates {
		if ctx.Err() != nil {
			complete = false
			break
		}
		namespaceKey := c.namespace
		if hostNamespace, e := os.Readlink(filepath.Join(o.options.Proc, "self", "ns", "net")); e == nil && hostNamespace == namespaceKey {
			namespaceKey = ""
		}
		key := c.core + "\x00" + c.binary + "\x00" + strings.TrimSuffix(strings.TrimSuffix(c.service, " (OpenRC)"), ".service") + "\x00" + namespaceKey + "\x00" + strings.Join(c.configs, "\x00")
		id := o.identity(key)
		item := proto.ObservedInstance{ID: id, Core: c.core, Source: "generic", Ownership: "external", PID: c.pid, Running: c.pid > 0, ProcessStart: c.start, ManagerPID: c.manager, ManagerRunning: c.managerRunning, Service: label(c.service, 128), Binary: label(c.binary, 1024), Namespace: label(c.namespace, 128), Ports: []proto.ObservedPort{}, Inbounds: []proto.ObservedInbound{}, ConfigPaths: []string{}, Issues: append([]string{}, c.issues...), StatsStatus: "not_configured"}
		if namespaceKey != "" {
			item.Issues = append(item.Issues, "container_network_namespace")
		}
		if o.options.ManagedInstance != nil && o.options.ManagedInstance(c.binary, c.service, c.configs) {
			item.Ownership = "managed"
		}
		if c.binary == "/etc/s-box/sing-box" && contains(c.configs, "/etc/s-box/sb.json") && strings.HasPrefix(c.service, "sing-box") {
			item.Source = "sing-box-yg"
		}
		if c.core == "xray" && strings.HasPrefix(c.binary, "/usr/local/x-ui/bin/") && strings.HasPrefix(c.service, "x-ui") {
			if st, err := os.Stat(filepath.Join(c.root, "/etc/x-ui-yg/x-ui-yg.db")); err == nil && st.Mode().IsRegular() {
				item.Source = "x-ui-yg"
				if c.database == "" {
					c.database = "/etc/x-ui-yg/x-ui-yg.db"
				}
			}
		}
		item.Version = version(ctx, c)
		for _, b := range o.options.Config.Bindings {
			if b.Binary == c.binary {
				item.Source = "manual"
				break
			}
		}
		if item.Version == "" {
			item.Issues = append(item.Issues, "version_unavailable")
		}
		var portsErr error
		if c.pid > 0 {
			item.Ports, portsErr = processPorts(o.options.Proc, c.pid)
			if portsErr != nil {
				if errors.Is(portsErr, errPortsLimit) {
					item.Truncated = true
					item.Issues = append(item.Issues, "ports_limit")
				} else {
					item.Issues = append(item.Issues, "ports_unavailable")
				}
			}
			if b, err := os.ReadFile(filepath.Join(o.options.Proc, strconv.Itoa(c.pid), "statm")); err == nil {
				fields := strings.Fields(string(b))
				if len(fields) > 1 {
					pages, _ := strconv.ParseInt(fields[1], 10, 64)
					rss := pages * int64(os.Getpagesize())
					if rss >= 0 {
						item.RSSBytes = &rss
					}
				}
			}
		}
		tags := map[string]string{}
		statsAddress := ""
		configOK := len(c.configs) > 0
		for configIndex, path := range c.configs {
			item.ConfigPaths = append(item.ConfigPaths, label(path, 1024))
			raw, err := readStable(filepath.Join(c.root, path), maxConfigBytes)
			if err != nil {
				configOK = false
				item.Issues = append(item.Issues, err.Error())
				continue
			}
			parsed, err := parseConfig(c.core, raw)
			if err != nil {
				configOK = false
				item.Issues = append(item.Issues, "config_invalid_jsonc")
				continue
			}
			item.Issues = append(item.Issues, parsed.Issues...)
			for key, value := range parsed.RawTags {
				if len(c.configs) > 1 {
					key = fmt.Sprintf("file%d-%s", configIndex, key)
				}
				tags[key] = value
			}
			if len(c.configs) > 1 {
				for i := range parsed.Inbounds {
					parsed.Inbounds[i].ID = fmt.Sprintf("file%d-%s", configIndex, parsed.Inbounds[i].ID)
				}
			}
			item.Inbounds = append(item.Inbounds, parsed.Inbounds...)
			if parsed.StatsAddress != "" {
				statsAddress = parsed.StatsAddress
			}
		}
		if len(c.configs) == 0 {
			item.Issues = append(item.Issues, "config_path_unknown")
		}
		if len(c.configs) > 1 {
			item.Issues = append(item.Issues, "multiple_config_application_unconfirmed")
		}
		if configOK {
			item.ConfigReadAt = o.now().Unix()
			item.LastSuccess = item.ConfigReadAt
		} else {
			item.Stale = true
			if old, ok := o.previous[id]; ok {
				item.Inbounds = append([]proto.ObservedInbound{}, old.Inbounds...)
				item.ConfigReadAt = old.ConfigReadAt
				item.LastSuccess = old.LastSuccess
			}
		}
		if item.Ownership != "managed" {
			o.readUsage(ctx, c, &item, tags, statsAddress)
		} else {
			item.StatsStatus = "managed_separately"
		}
		if len(item.Inbounds) > min(proto.ProxyMaxInbounds, budget) {
			item.Inbounds = item.Inbounds[:min(proto.ProxyMaxInbounds, budget)]
			item.Truncated = true
			item.Issues = append(item.Issues, "inbounds_limit")
		}
		budget -= len(item.Inbounds)
		for i := range item.Inbounds {
			if c.pid > 0 && portsErr == nil {
				observed := false
				for _, p := range item.Ports {
					if boundPortMatches(item.Inbounds[i], p) {
						observed = true
					}
				}
				item.Inbounds[i].Listening = &observed
			}
		}
		if c.pid > 0 {
			again, _, err := processInfo(o.options.Proc, c.pid)
			if err != nil || again.start != c.start || again.binary != c.binary {
				item.Stale = true
				item.Issues = append(item.Issues, "process_changed_during_read")
			}
		}
		current[id] = item
	}
	o.previous = current
	return sortedInstances(current), complete
}

func boundPortMatches(in proto.ObservedInbound, p proto.ObservedPort) bool {
	if strconv.Itoa(p.Port) != in.Port {
		return false
	}
	ip := net.ParseIP(in.Listen)
	bound := net.ParseIP(p.Address)
	if ip != nil && !ip.IsUnspecified() && (bound == nil || !ip.Equal(bound)) {
		return false
	}
	network := ""
	switch in.Protocol {
	case "hysteria", "hysteria2", "tuic":
		network = "udp"
	case "vless", "vmess", "trojan", "anytls":
		network = "tcp"
	}
	return network == "" || network == p.Network
}

func contains(items []string, value string) bool {
	for _, v := range items {
		if v == value {
			return true
		}
	}
	return false
}

func processPorts(proc string, pid int) ([]proto.ObservedPort, error) {
	dir := filepath.Join(proc, strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return []proto.ObservedPort{}, err
	}
	inodes := map[string]bool{}
	for _, fd := range fds {
		target, e := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if e == nil && strings.HasPrefix(target, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	out := []proto.ObservedPort{}
	seen := map[string]bool{}
	for _, table := range []string{"tcp", "tcp6", "udp", "udp6"} {
		b, e := os.ReadFile(filepath.Join(dir, "net", table))
		if e != nil {
			if os.IsNotExist(e) {
				continue
			}
			return out, e
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || !inodes[f[9]] {
				continue
			}
			if strings.HasPrefix(table, "tcp") && f[3] != "0A" {
				continue
			}
			if strings.HasPrefix(table, "udp") && f[3] != "07" {
				continue
			}
			addr, port, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			n, e := strconv.ParseInt(port, 16, 32)
			if e != nil || n < 1 || n > 65535 {
				continue
			}
			ip, e := hex.DecodeString(addr)
			if e != nil || len(ip) != 4 && len(ip) != 16 {
				continue
			}
			for i := 0; i < len(ip); i += 4 {
				ip[i], ip[i+3] = ip[i+3], ip[i]
				ip[i+1], ip[i+2] = ip[i+2], ip[i+1]
			}
			network := table[:3]
			key := fmt.Sprintf("%s:%d/%s", net.IP(ip), n, network)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, proto.ObservedPort{Address: net.IP(ip).String(), Port: int(n), Network: network})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port == out[j].Port {
			return out[i].Network < out[j].Network
		}
		return out[i].Port < out[j].Port
	})
	if len(out) > 512 {
		return out[:512], errPortsLimit
	}
	return out, nil
}
