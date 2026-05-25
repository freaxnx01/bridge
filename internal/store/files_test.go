package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := AtomicWrite(p, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != `{"a":1}` {
		t.Errorf("got %q", b)
	}
}

func TestAtomicWriteReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	_ = os.WriteFile(p, []byte("old"), 0o644)
	if err := AtomicWrite(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new" {
		t.Errorf("got %q", b)
	}
}

func TestReadFileMissingIsEmpty(t *testing.T) {
	b, err := ReadFile(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("expected empty, got %q", b)
	}
}

func TestReadFileExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	_ = os.WriteFile(p, []byte("hi"), 0o644)
	b, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hi" {
		t.Errorf("got %q", b)
	}
}

func TestAtomicWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "nested", "x.json")
	if err := AtomicWrite(p, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected file: %v", err)
	}
}
