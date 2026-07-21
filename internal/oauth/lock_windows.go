//go:build windows

package oauth

import (
	"fmt"
	"os"
	"path/filepath"
)

// fileLock uses exclusive file creation on Windows, where flock(2) has no
// equivalent. A stale lock after a crash must be removed by hand; that is
// acceptable because the MCP server is deployed on Linux and this build
// exists so cross-compilation keeps working.
type fileLock struct{ path string }

func acquireLock(dir string) (*fileLock, error) {
	path := filepath.Join(dir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("another bridge instance is using state dir %s (or a stale %s remains): %w", dir, path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close lock file: %w", err)
	}
	return &fileLock{path: path}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.path == "" {
		return nil
	}
	return os.Remove(l.path)
}
