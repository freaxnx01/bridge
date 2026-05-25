package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLockExcludesConcurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")

	var holdMu sync.Mutex
	held := false

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l, err := AcquireLock(p)
		if err != nil {
			t.Errorf("first acquire: %v", err)
			return
		}
		holdMu.Lock()
		held = true
		holdMu.Unlock()
		time.Sleep(200 * time.Millisecond)
		holdMu.Lock()
		held = false
		holdMu.Unlock()
		_ = l.Release()
	}()

	time.Sleep(50 * time.Millisecond)

	l2, err := AcquireLock(p)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	holdMu.Lock()
	if held {
		t.Error("second acquire returned while first still held lock")
	}
	holdMu.Unlock()
	_ = l2.Release()
	wg.Wait()
}
