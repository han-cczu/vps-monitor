//go:build !linux

package selfupdate

import "fmt"

func replaceProcess(string) error { return fmt.Errorf("agent self update requires Linux") }
