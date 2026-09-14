package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"vpsmon/agent/internal/config"
	"vpsmon/agent/internal/corectl"
	"vpsmon/proto"
)

func runCore(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vps-agent core apply|state|stats|install|start|stop|restart|logs [flags]")
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("corectl requires Linux with systemd")
	}
	f := flag.NewFlagSet("core "+args[0], flag.ContinueOnError)
	path := f.String("config", config.DefaultPath, "agent YAML (needed for install)")
	file := f.String("file", "", "sing-box config JSON (apply)")
	v := f.String("version", "", "core version vX.Y.Z (install/apply)")
	sha := f.String("sha256", "", "binary SHA256 (install)")
	ports := f.String("ports", "", "comma-separated port/protocol list (apply)")
	rev := f.Int64("revision", 0, "revision (apply; default last applied + 1)")
	lines := f.Int("lines", 200, "log tail lines (1..1000)")
	address := f.String("stats-address", "", "loopback stats IP:port; overrides agent YAML")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	cfg, err := loadConfig(*path, true)
	if err != nil {
		return err
	}
	if *address != "" {
		cfg.Core.StatsAddress = *address
	}
	m, err := corectl.New(corectl.Options{Server: cfg.Server, Token: cfg.Token, StatsAddress: cfg.Core.StatsAddress})
	if err != nil {
		return err
	}
	defer m.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var out any
	switch args[0] {
	case "state":
		out = m.State(ctx)
	case "stats":
		out, err = m.Stats(ctx)
	case "logs":
		var txt string
		txt, err = corectl.Tail(corectl.DefaultPaths().Log, *lines)
		out = proto.CoreLogs{Type: proto.TypeCoreLogs, Kind: "error", Text: txt}
	case "apply":
		var raw []byte
		raw, err = os.ReadFile(*file)
		if err != nil {
			return err
		}
		var sum string
		raw, sum, err = corectl.CompactConfig(raw)
		if err != nil {
			return err
		}
		if *rev == 0 {
			*rev = m.AppliedRevision() + 1
		}
		ps := []string{}
		if *ports != "" {
			ps = strings.Split(*ports, ",")
		}
		out, err = m.Execute(ctx, proto.CoreApply{Type: proto.TypeCoreApply, Core: "sing-box", Revision: *rev, Version: *v, ConfigSHA256: sum, Ports: ps, Config: raw})
	case "install", "start", "stop", "restart":
		out, err = m.Execute(ctx, proto.CoreAction{Type: proto.TypeCoreAction, Action: args[0], Version: *v, SHA256: *sha})
	default:
		return fmt.Errorf("unknown core subcommand")
	}
	if out != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if e := enc.Encode(out); e != nil {
			return e
		}
	}
	return err
}
