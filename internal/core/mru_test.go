package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMRU(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mru")
	_ = os.WriteFile(p, []byte("/a\n/b\n/a\n/c\n"), 0o644)
	paths, err := LoadMRU(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/c", "/a", "/b"}
	if len(paths) != len(want) {
		t.Fatalf("got %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("[%d] got %s want %s", i, paths[i], want[i])
		}
	}
}

func TestLoadMRUMissing(t *testing.T) {
	paths, err := LoadMRU(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("want empty, got %v", paths)
	}
}

func TestLoadMRUSkipsBlank(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mru")
	_ = os.WriteFile(p, []byte("/a\n\n  \n/b\n"), 0o644)
	paths, _ := LoadMRU(p)
	if len(paths) != 2 || paths[0] != "/b" || paths[1] != "/a" {
		t.Errorf("got %v", paths)
	}
}
