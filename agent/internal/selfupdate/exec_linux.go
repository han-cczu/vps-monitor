//go:build linux

package selfupdate

import (
	"os"
	"syscall"
)

func replaceProcess(binary string) error {
	return syscall.Exec(binary, append([]string{binary}, os.Args[1:]...), os.Environ())
}
