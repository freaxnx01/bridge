package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMRUTouchAppendsLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mru")
	if err := MRUTouch(p, "/a"); err != nil {
		t.Fatal(err)
	}
	if err := MRUTouch(p, "/b"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 || lines[0] != "/a" || lines[1] != "/b" {
		t.Errorf("got %v", lines)
	}
}

func TestMRUTouchCreatesParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "mru")
	if err := MRUTouch(p, "/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestMRUTouchRejectsEmptyPath(t *testing.T) {
	if err := MRUTouch(filepath.Join(t.TempDir(), "mru"), ""); err == nil {
		t.Error("expected error on empty path")
	}
}

func TestMRUTouchHoldsLockDuringWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mru")
	lock, err := AcquireLock(p + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- MRUTouch(p, "/a")
	}()
	select {
	case <-done:
		t.Fatal("MRUTouch returned while lock held")
	case <-time.After(100 * time.Millisecond):
		// good
	}
	_ = lock.Release()
	if err := <-done; err != nil {
		t.Errorf("MRUTouch after release: %v", err)
	}
}
