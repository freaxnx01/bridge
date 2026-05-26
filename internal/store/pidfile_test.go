package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritePIDFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sync.pid")
	if err := WritePIDFile(p, 12345); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPIDFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 12345 {
		t.Errorf("got %d", pid)
	}
}

func TestReadPIDFileMissing(t *testing.T) {
	pid, err := ReadPIDFile(filepath.Join(t.TempDir(), "missing.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if pid != 0 {
		t.Errorf("expected 0 for missing, got %d", pid)
	}
}

func TestIsPIDRunningSelf(t *testing.T) {
	if !IsPIDRunning(os.Getpid()) {
		t.Error("expected self to be running")
	}
}

func TestIsPIDRunningBogus(t *testing.T) {
	if IsPIDRunning(0) {
		t.Error("PID 0 should report not running")
	}
	if IsPIDRunning(99999999) {
		t.Error("nonexistent PID should report not running")
	}
}

func TestAcquirePIDFileSucceedsWhenAbsent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pid")
	release, err := AcquirePIDFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	pid, _ := ReadPIDFile(p)
	if pid != os.Getpid() {
		t.Errorf("got pid %d, want %d", pid, os.Getpid())
	}
}

func TestAcquirePIDFileRejectsLiveHolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pid")
	release, err := AcquirePIDFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := AcquirePIDFile(p); err != ErrAlreadyRunning {
		t.Errorf("got %v, want ErrAlreadyRunning", err)
	}
}

func TestAcquirePIDFileReclaimsStale(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pid")
	_ = WritePIDFile(p, 99999998)
	release, err := AcquirePIDFile(p)
	if err != nil {
		t.Fatalf("got %v, want success after stale", err)
	}
	defer release()
}

func TestRemovePIDFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pid")
	_ = WritePIDFile(p, 1)
	if err := RemovePIDFile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("expected file gone, stat err = %v", err)
	}
	if err := RemovePIDFile(p); err != nil {
		t.Errorf("second remove: %v", err)
	}
}
