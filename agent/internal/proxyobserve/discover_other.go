//go:build !linux

package proxyobserve

import (
	"context"
	"os"
	"vpsmon/proto"
)

func openSource(path string) (*os.File, error) { return os.Open(path) }
func ExternalPresent() bool                    { return true }
func (o *Observer) Scan(context.Context) ([]proto.ObservedInstance, bool) {
	return []proto.ObservedInstance{}, false
}
