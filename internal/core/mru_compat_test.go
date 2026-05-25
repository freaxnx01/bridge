package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors the format bash bridge writes today.
func TestMRUBashCompat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mru")
	if err := os.WriteFile(p, []byte(
		"/home/me/projects/repos/github/me/public/old-thing\n"+
			"/home/me/projects/repos/github/me/private/secret\n"+
			"/home/me/projects/repos/github/me/public/bridge\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMRU(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/home/me/projects/repos/github/me/public/bridge",
		"/home/me/projects/repos/github/me/private/secret",
		"/home/me/projects/repos/github/me/public/old-thing",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] %s vs %s", i, got[i], want[i])
		}
	}
}
