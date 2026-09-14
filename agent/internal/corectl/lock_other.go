//go:build !linux && !windows

package corectl

import "fmt"

func fileLock(string) (func(), error) { return nil, fmt.Errorf("corectl requires Linux with systemd") }
