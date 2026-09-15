package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"time"
	"vpsmon/agent/internal/config"
	"vpsmon/agent/internal/proxyobserve"
)

// This diagnostic command never constructs the managed-core controller.
func runObserve(args []string) error {
	f := flag.NewFlagSet("observe", flag.ContinueOnError)
	path := f.String("config", config.DefaultPath, "Agent YAML")
	state := f.String("state-dir", "/var/lib/vps-agent/observe", "观察器自身缓存目录")
	if err := f.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*path, true)
	if err != nil {
		return err
	}
	o, err := proxyobserve.New(proxyobserve.Options{Config: cfg.ProxyObserve, StateDir: *state})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, complete := o.Scan(ctx)
	return json.NewEncoder(os.Stdout).Encode(struct {
		Complete  bool `json:"complete"`
		Instances any  `json:"instances"`
	}{complete, items})
}
