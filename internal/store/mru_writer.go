package store

import (
	"errors"
	"os"
	"path/filepath"
)

// MRUTouch appends path to the MRU file. The most-recently-used path is the
// last line. Concurrent writers serialize via flock on "<file>.lock".
//
// Append-only: dedupe happens at read time (see core.LoadMRU). Keeps writes
// O(1) and crash-safe — no rewrite, so a torn write can only ever drop the
// current append, never corrupt earlier history.
func MRUTouch(path, target string) error {
	if target == "" {
		return errors.New("MRUTouch: empty target")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := AcquireLock(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Release()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(target + "\n"); err != nil {
		return err
	}
	return f.Sync()
}
