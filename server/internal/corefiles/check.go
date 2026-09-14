package corefiles

import (
	"context"
	"path/filepath"

	"vpsmon/server/internal/proxy/render"
)

// CheckConfig pins the exact immutable version while checking, including an old
// rollback version. Selecting/deleting a core cannot race the executable lookup.
func (s *Store) CheckConfig(ctx context.Context, version string, config []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, _, err := s.open(version, s.arch)
	if err != nil {
		return err
	}
	f.Close()
	return render.Check(ctx, filepath.Join(s.root.Name(), filename(version, s.arch)), config)
}

func (s *Store) CheckConfigDetailed(ctx context.Context, version string, config []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, _, err := s.open(version, s.arch)
	if err != nil {
		return err
	}
	f.Close()
	return render.CheckDetailed(ctx, filepath.Join(s.root.Name(), filename(version, s.arch)), config)
}
