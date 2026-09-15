//go:build linux

package proxyobserve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"vpsmon/proto"
)

func TestConfigArgumentsDirectoryOrder(t *testing.T) {
	files, dirs := configArguments([]string{"sing-box", "run", "-c", "a.json", "-C=conf", "-D", "/opt/work"}, "/root")
	if !reflect.DeepEqual(files, []string{"/opt/work/a.json"}) || !reflect.DeepEqual(dirs, []string{"/opt/work/conf"}) {
		t.Fatal(files, dirs)
	}
}
func fakeProcess(t *testing.T, proc, root string, pid int, core string, args []string) {
	t.Helper()
	dir := filepath.Join(proc, strconv.Itoa(pid))
	for _, p := range []string{"fd", "net", "ns"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for p, target := range map[string]string{"exe": core, "root": root, "cwd": "/opt", "ns/net": "net:[1]"} {
		if err := os.Symlink(target, filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	fields := append([]string{"S", "1"}, make([]string, 18)...)
	for i := 2; i < len(fields); i++ {
		fields[i] = "0"
	}
	fields[19] = "1234"
	for p, data := range map[string]string{"stat": strconv.Itoa(pid) + " (xray with space) " + strings.Join(fields, " "), "statm": "10 2", "cmdline": strings.Join(args, "\x00") + "\x00", "cgroup": "0::/system.slice/custom@one.service"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestMultipleProcessesStaleConfigAndPIDPorts(t *testing.T) {
	proc := t.TempDir()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(proc, "self/ns"), 0700)
	os.Symlink("net:[1]", filepath.Join(proc, "self/ns/net"))
	os.MkdirAll(filepath.Join(root, "opt"), 0700)
	configPath := filepath.Join(root, "opt/config.json")
	raw := `{"inbounds":[{"tag":"t","protocol":"vless","port":1234,"listen":"127.0.0.1"}]}`
	os.WriteFile(configPath, []byte(raw), 0600)
	fakeProcess(t, proc, root, 100, "/opt/xray", []string{"xray", "run", "-c", "config.json"})
	fakeProcess(t, proc, root, 200, "/opt/renamed", []string{"renamed", "run", "-c", "config.json"})
	os.Symlink("socket:[99]", filepath.Join(proc, "100/fd/7"))
	os.WriteFile(filepath.Join(proc, "100/net/tcp"), []byte("header\n 0: 0100007F:04D2 00000000:0000 0A 0:0 0:0 0 0 0 99\n 1: 0100007F:04D3 00000000:0000 0A 0:0 0:0 0 0 0 100\n"), 0600)
	o, err := New(Options{Proc: proc, StateDir: t.TempDir(), Config: Config{Bindings: []Binding{{Core: "xray", Binary: "/opt/renamed"}}}})
	if err != nil {
		t.Fatal(err)
	}
	items, complete := o.Scan(context.Background())
	if !complete || len(items) != 2 {
		t.Fatal(items, complete)
	}
	var first proto.ObservedInstance
	for _, i := range items {
		if i.PID == 100 {
			first = i
		}
	}
	if len(first.Ports) != 1 || len(first.Inbounds) != 1 || first.Inbounds[0].Listening == nil || !*first.Inbounds[0].Listening {
		t.Fatal("port ownership not resolved", first)
	}
	os.WriteFile(configPath, []byte("invalid now"), 0600)
	again, _ := o.Scan(context.Background())
	for _, i := range again {
		if !i.Stale || len(i.Inbounds) != 1 {
			t.Fatal("failed parse lost snapshot")
		}
	}
	if boundPortMatches(proto.ObservedInbound{Port: "1234", Protocol: "tuic"}, proto.ObservedPort{Port: 1234, Network: "tcp"}) {
		t.Fatal("TCP misidentified as QUIC")
	}
}
