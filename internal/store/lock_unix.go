//go:build !windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type unixLock struct {
	f *os.File
}

func (l *unixLock) Release() error {
	err := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	cerr := l.f.Close()
	if err != nil {
		return err
	}
	return cerr
}

func AcquireLock(path string) (Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &unixLock{f: f}, nil
}
