package store

import (
	"errors"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path via a tmp-file + rename in the same directory.
// Creates parent directories with mode 0o755 if needed.
func AtomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()      // best-effort close; returning the write error
		_ = os.Remove(tmp) // best-effort cleanup of the failed tmp file
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()      // best-effort close; returning the sync error
		_ = os.Remove(tmp) // best-effort cleanup of the failed tmp file
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup of the failed tmp file
		return err
	}
	return os.Rename(tmp, path)
}

// ReadFile reads path. Returns empty bytes and nil error if the file is missing.
func ReadFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}
