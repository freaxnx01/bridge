package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReposFromDiscovery(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"github/freaxnx01/public/bridge",
		"github/freaxnx01/private/secret",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := loadRepos(root)
	if len(got) != 2 {
		t.Fatalf("expected 2 repos, got %d (%v)", len(got), got)
	}
	var sawPub, sawPri bool
	for _, r := range got {
		if strings.HasSuffix(r.name, "bridge") && r.vis == "pub" {
			sawPub = true
		}
		if strings.HasSuffix(r.name, "secret") && r.vis == "pri" {
			sawPri = true
		}
	}
	if !sawPub || !sawPri {
		t.Errorf("vis-tag mapping wrong: %+v", got)
	}
}

func TestLoadReposMissingRootReturnsEmpty(t *testing.T) {
	if got := loadRepos("/no/such/dir/here"); got != nil && len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestRunOnceRendersFrame(t *testing.T) {
	// --once must produce output without a TTY; this is the smoke-test
	// surface CI uses.
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "github/freaxnx01/public/bridge"), 0o755)
	// Capture stdout
	r, w, _ := os.Pipe()
	saved := os.Stdout
	os.Stdout = w
	err := Run(root, true)
	w.Close()
	os.Stdout = saved
	if err != nil {
		t.Fatalf("Run --once: %v", err)
	}
	buf := make([]byte, 32*1024)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "bridge") {
		t.Errorf("rendered frame missing repo name 'bridge':\n%s", got)
	}
	if !strings.Contains(got, "Repos") {
		t.Errorf("rendered frame missing 'Repos' panel title:\n%s", got)
	}
}
