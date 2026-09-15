//go:build linux

package proxyobserve

import (
	"context"
	"os"
	"path/filepath"
	"vpsmon/proto"
)

func (o *Observer) readUsage(ctx context.Context, c candidate, item *proto.ObservedInstance, tags map[string]string, address string) {
	// Clear prior statistics before collecting: stale configs must not silently
	// carry an old usage value with a fresh collection timestamp.
	for i := range item.Inbounds {
		item.Inbounds[i].Usage = nil
	}
	if c.database != "" {
		if err := readManagerUsage(ctx, filepath.Join(c.root, c.database), filepath.Join(o.options.StateDir, "scratch"), item, tags, o.now().Unix()); err != nil {
			item.StatsStatus = "unavailable"
			item.Issues = append(item.Issues, err.Error())
			for i := range item.Inbounds {
				item.Inbounds[i].Usage = nil
			}
		} else {
			item.StatsStatus = "manager_total"
		}
		return
	}
	if address == "" {
		item.StatsStatus = "not_configured"
		return
	}
	host, e := os.Readlink(filepath.Join(o.options.Proc, "self", "ns", "net"))
	if e != nil || c.namespace == "" || c.namespace != host {
		item.StatsStatus = "unavailable"
		item.Issues = append(item.Issues, "stats_namespace_unsupported")
		return
	}
	stats, err := queryReferenceStats(ctx, c.core, address)
	if err != nil {
		item.StatsStatus = "unavailable"
		item.Issues = append(item.Issues, "stats_read_failed")
		return
	}
	applyReferenceStats(item.Inbounds, tags, stats, c.core, o.now().Unix())
	item.StatsStatus = "reference"
}
