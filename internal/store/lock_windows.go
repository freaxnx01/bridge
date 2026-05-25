//go:build windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type winLock struct {
	f *os.File
}

func (l *winLock) Release() error {
	h := windows.Handle(l.f.Fd())
	var ol windows.Overlapped
	err := windows.UnlockFileEx(h, 0, ^uint32(0), ^uint32(0), &ol)
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
	h := windows.Handle(f.Fd())
	var ol windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &ol); err != nil {
		f.Close()
		return nil, err
	}
	return &winLock{f: f}, nil
}
