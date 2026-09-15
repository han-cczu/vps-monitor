//go:build !linux

package corectl

import "os"

func (m *Manager) managedPlatform() bool { return m.paths.Unit != DefaultPaths().Unit }

func (m *Manager) verifyRuntime() error                { return nil }
func openLogForTruncate(path string) (*os.File, error) { return os.OpenFile(path, os.O_WRONLY, 0) }
