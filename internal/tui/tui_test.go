package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
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

func TestLoadIssuesFromCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "issues.json")
	cache := forge.IssueCache{
		UpdatedAt: time.Now(),
		Issues: []forge.Issue{
			{Forge: "github", Repo: "freaxnx01/bridge", Number: 64, Title: "feat(tui): promote dashboard"},
			{Forge: "github", Repo: "freaxnx01/bridge", Number: 70, Title: "feat(tui): wire Open Issues panel"},
		},
	}
	b, _ := json.Marshal(cache)
	if err := os.WriteFile(cachePath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadIssues(cachePath)
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d (%v)", len(got), got)
	}
	if got[0].num != 64 || got[1].num != 70 {
		t.Errorf("issue numbers not preserved: %v", got)
	}
}

func TestLoadIssuesMissingCacheReturnsEmpty(t *testing.T) {
	if got := loadIssues("/no/such/issues.json"); len(got) != 0 {
		t.Errorf("expected empty on missing cache, got %v", got)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestActOnSelectionRepo(t *testing.T) {
	m := initialModel(
		[]repo{{name: "bridge", path: "/tmp/repos/bridge", vis: "pub"}},
		nil, nil,
	)
	m.focus = paneRepos
	newM, cmd := m.actOnSelection()
	if cmd == nil {
		t.Fatalf("expected tea.Quit command")
	}
	got := newM.(model).action
	if got.kind != actLaunchRepo {
		t.Errorf("expected actLaunchRepo, got %d", got.kind)
	}
	wantArgv := []string{"tmux", "new-session", "-A", "-s", "bridge", "-c", "/tmp/repos/bridge"}
	if strings.Join(got.argv, " ") != strings.Join(wantArgv, " ") {
		t.Errorf("argv mismatch: got %v, want %v", got.argv, wantArgv)
	}
}

func TestActOnSelectionIssueWithoutURL(t *testing.T) {
	m := initialModel(nil, []issue{{num: 64, repo: "x/y", title: "t", url: ""}}, nil)
	m.focus = paneIssues
	newM, cmd := m.actOnSelection()
	if cmd != nil {
		t.Errorf("expected nil cmd when URL is missing (no quit)")
	}
	if newM.(model).action.kind != actNone {
		t.Errorf("expected actNone when URL missing")
	}
	if !strings.Contains(newM.(model).status, "no URL") {
		t.Errorf("expected status hint about missing URL, got %q", newM.(model).status)
	}
}

func TestActOnSelectionSession(t *testing.T) {
	m := initialModel(nil, nil, []session{{name: "bridge", state: "detached", age: "1h"}})
	m.focus = paneSessions
	newM, cmd := m.actOnSelection()
	if cmd == nil {
		t.Fatalf("expected tea.Quit command")
	}
	got := newM.(model).action
	wantArgv := []string{"tmux", "attach-session", "-t", "bridge"}
	if strings.Join(got.argv, " ") != strings.Join(wantArgv, " ") {
		t.Errorf("argv mismatch: got %v, want %v", got.argv, wantArgv)
	}
}

func TestTmuxSafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"bridge", "bridge"},
		{"public/bridge", "public-bridge"},
		{"owner/Repo.Name", "owner-Repo.Name"},
		{"", "repo"},
	}
	for _, c := range cases {
		if got := tmuxSafe(c.in); got != c.want {
			t.Errorf("tmuxSafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoadSessionsToleratesMissingSlots(t *testing.T) {
	// loadSessions must not panic when slots.json is missing. The host's
	// tmux state is non-deterministic across CI/dev, so we just assert
	// the call returns and every result row has a non-empty name
	// (cross-referenced or not).
	for _, s := range loadSessions("/no/such/slots.json") {
		if s.name == "" {
			t.Errorf("session row with empty name: %+v", s)
		}
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
	err := Run(root, "", "", true)
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
