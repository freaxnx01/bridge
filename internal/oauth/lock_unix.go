//go:build unix

package oauth

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock is an advisory OS lock ensuring only one bridge process uses a
// given state directory. Concurrent writers would silently clobber state.
type fileLock struct{ f *os.File }

func acquireLock(dir string) (*fileLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// best-effort close; the primary error below is the actionable one
		_ = f.Close()
		return nil, fmt.Errorf("another bridge instance is using state dir %s: %w", dir, err)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		// best-effort close; the primary error below is the actionable one
		_ = l.f.Close()
		return fmt.Errorf("unlock: %w", err)
	}
	return l.f.Close()
}
