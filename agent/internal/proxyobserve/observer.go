package proxyobserve

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsmon/proto"
)

type Binding struct {
	Core        string   `yaml:"core" json:"core"`
	Service     string   `yaml:"service" json:"service"`
	Binary      string   `yaml:"binary" json:"binary"`
	ConfigPaths []string `yaml:"config_paths" json:"config_paths"`
	Database    string   `yaml:"database" json:"database"`
}
type Config struct {
	Disabled bool      `yaml:"disabled"`
	Bindings []Binding `yaml:"bindings"`
}

func (c Config) Validate() error {
	if len(c.Bindings) > proto.ProxyMaxInstances {
		return errors.New("proxy_observe bindings exceed 32")
	}
	for _, b := range c.Bindings {
		if b.Core != "sing-box" && b.Core != "xray" || len(b.Service) > 128 || strings.ContainsAny(b.Service, "\n\r\x00") || len(b.ConfigPaths) > 16 || !path.IsAbs(b.Binary) {
			return errors.New("invalid proxy_observe binding")
		}
		for _, p := range append(append([]string{b.Binary}, b.ConfigPaths...), b.Database) {
			if p != "" && (!path.IsAbs(p) || len(p) > 1024 || strings.ContainsAny(p, "\n\r\x00")) {
				return errors.New("proxy_observe requires absolute local paths")
			}
		}
	}
	return nil
}

type Options struct {
	Config          Config
	StateDir        string
	Proc            string
	Send            func(any) error
	ManagedInstance func(string, string, []string) bool
}
type Observer struct {
	options  Options
	mu       sync.Mutex
	session  string
	seq      int64
	refresh  chan struct{}
	ids      map[string]string
	previous map[string]proto.ObservedInstance
	now      func() time.Time
}

func New(o Options) (*Observer, error) {
	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	if o.StateDir == "" {
		o.StateDir = "/var/lib/vps-agent/observe"
	}
	if o.Proc == "" {
		o.Proc = "/proc"
	}
	x := &Observer{options: o, refresh: make(chan struct{}, 1), ids: map[string]string{}, previous: map[string]proto.ObservedInstance{}, now: time.Now}
	if b, err := readStable(filepath.Join(o.StateDir, "instances.json"), 256<<10); err == nil {
		_ = json.Unmarshal(b, &x.ids)
	}
	if x.ids == nil {
		x.ids = map[string]string{}
	}
	return x, nil
}
func shortHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:12]) }
func (o *Observer) identity(key string) string {
	key = shortHash(key)
	if id := o.ids[key]; id != "" {
		return id
	}
	if len(o.ids) >= 2048 {
		return "h" + key
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return shortHash(key)
	}
	id := hex.EncodeToString(b[:])
	o.ids[key] = id
	if os.MkdirAll(o.options.StateDir, 0700) == nil {
		data, _ := json.Marshal(o.ids)
		f, err := os.CreateTemp(o.options.StateDir, ".identities-*")
		if err == nil {
			name := f.Name()
			_ = f.Chmod(0600)
			_, err = f.Write(data)
			if err == nil {
				err = f.Sync()
			}
			_ = f.Close()
			if err == nil {
				err = os.Rename(name, filepath.Join(o.options.StateDir, "instances.json"))
			}
			if err != nil {
				_ = os.Remove(name)
			}
		}
	}
	return id
}
func (o *Observer) Negotiate(session string) {
	o.mu.Lock()
	changed := session != o.session
	o.session = session
	if changed {
		o.seq = 0
	}
	o.mu.Unlock()
	if changed {
		o.Refresh()
	}
}
func (o *Observer) Refresh() {
	select {
	case o.refresh <- struct{}{}:
	default:
	}
}
func (o *Observer) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-o.refresh:
		}
		o.mu.Lock()
		session := o.session
		o.seq++
		seq := o.seq
		o.mu.Unlock()
		if session == "" || o.options.Config.Disabled {
			continue
		}
		items, complete := o.Scan(ctx)
		pages := observationPages(items, session, seq, complete, o.now().Unix())
		for _, p := range pages {
			if o.options.Send != nil && o.options.Send(p) != nil {
				break
			}
		}
	}
}
func observationPages(items []proto.ObservedInstance, session string, seq int64, complete bool, at int64) []proto.ProxyObservation {
	pages := []proto.ProxyObservation{}
	for _, item := range items {
		inbounds := item.Inbounds
		for start := 0; start < len(inbounds) || start == 0; start += 64 {
			item.Inbounds = inbounds[start:min(start+64, len(inbounds))]
			pages = append(pages, proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: session, Sequence: seq, ScanComplete: complete, CollectedAt: at, Instances: []proto.ObservedInstance{item}})
			if len(inbounds) == 0 {
				break
			}
		}
	}
	if len(pages) == 0 {
		pages = append(pages, proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: session, Sequence: seq, ScanComplete: complete, CollectedAt: at, Instances: []proto.ObservedInstance{}})
	}
	for i := range pages {
		pages[i].Page = i
		pages[i].Pages = len(pages)
	}
	return pages
}
func sortedInstances(m map[string]proto.ObservedInstance) []proto.ObservedInstance {
	out := make([]proto.ObservedInstance, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
