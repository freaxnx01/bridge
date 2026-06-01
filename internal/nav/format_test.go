package nav

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func TestHumanLastAccessed_TwoUnitsMax(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "0m"},
		{"minutes", 4 * time.Minute, "4m"},
		{"hours-minutes", 3*time.Hour + 12*time.Minute, "3h 12m"},
		{"days-hours", 26*time.Hour + 20*time.Minute, "1d 2h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanLastAccessed(tt.d); got != tt.want {
				t.Errorf("humanLastAccessed(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFilterRepos_CaseInsensitiveSubstring(t *testing.T) {
	rows := []repoRow{
		{label: "github/public/bridge"},
		{label: "github/public/ai-instructions"},
		{label: "gitlab/acme/infra-tools"},
	}
	got := filterRepos(rows, "INFRA")
	if len(got) != 1 || got[0].label != "gitlab/acme/infra-tools" {
		t.Fatalf("filterRepos = %+v, want only infra-tools", got)
	}
	if len(filterRepos(rows, "")) != 3 {
		t.Errorf("empty filter should return all rows")
	}
}

func TestBuildDashRows_MatchesSessionsAndSorts(t *testing.T) {
	repo := core.Repo{Name: "bridge"}
	wts := []worktree.Entry{
		{Path: "/r/.worktrees/fix-x", Branch: "worktree-fix-x"},
		{Path: "/r/.worktrees/docs", Branch: "worktree-docs"},
		{Path: "/r/.worktrees/spike", Branch: "worktree-spike"},
	}
	slots := []core.Slot{
		{ID: "s-fix", Repo: "bridge", Worktree: "fix-x", Agent: "claude"},
		{ID: "s-docs", Repo: "freaxnx01/bridge", Worktree: "docs", Agent: "copilot"},
	}
	sessions := []core.Session{
		{SlotID: "s-fix", State: "attached", LastActivity: time.Unix(1000, 0)},
		{SlotID: "s-docs", State: "detached", LastActivity: time.Unix(2000, 0)},
	}
	now := time.Unix(3000, 0)
	got := buildDashRows(repo, wts, slots, sessions, now)

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	// Sessioned rows first, sorted by last-accessed DESC (docs@2000 before fix@1000),
	// then session-less worktrees (spike).
	if got[0].worktree != "docs" || !got[0].hasSession || got[0].agent != "copilot" {
		t.Errorf("row[0] = %+v, want docs/copilot/hasSession", got[0])
	}
	if got[1].worktree != "fix-x" || got[1].state != "attached" {
		t.Errorf("row[1] = %+v, want fix-x/attached", got[1])
	}
	if got[2].worktree != "spike" || got[2].hasSession {
		t.Errorf("row[2] = %+v, want spike with no session", got[2])
	}
}

func TestParseDirtyStatus(t *testing.T) {
	tests := []struct {
		name, out       string
		modified, ahead int
		clean           bool
	}{
		{"clean tracked", "## main...origin/main\n", 0, 0, true},
		{"dirty no ahead", "## main...origin/main\n M a.go\n?? b.go\n", 2, 0, false},
		{"ahead only", "## main...origin/main [ahead 3]\n", 0, 3, true},
		{"ahead+behind+dirty", "## wt...origin/wt [ahead 2, behind 1]\n M x\n", 1, 2, false},
		{"no upstream", "## worktree-fix\n M x\n", 1, 0, false},
		{"empty", "", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDirtyStatus(tt.out)
			if got.modified != tt.modified || got.ahead != tt.ahead || got.clean != tt.clean {
				t.Errorf("parseDirtyStatus(%q) = %+v, want modified=%d ahead=%d clean=%v", tt.out, got, tt.modified, tt.ahead, tt.clean)
			}
		})
	}
}

func TestSortRepoRows_AscCaseInsensitiveIgnoringRemotePrefix(t *testing.T) {
	rows := []repoRow{
		{label: "github/public/Zebra"},
		{label: "↓ github/public/apple"},
		{label: "github/public/Mango"},
	}
	sortRepoRows(rows)
	got := []string{rows[0].label, rows[1].label, rows[2].label}
	want := []string{"↓ github/public/apple", "github/public/Mango", "github/public/Zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortRepoRows = %v, want %v", got, want)
		}
	}
}

func TestWindowAround(t *testing.T) {
	tests := []struct{ n, sel, size, ws, we int }{
		{5, 0, 10, 0, 5},
		{100, 0, 10, 0, 10},
		{100, 50, 10, 45, 55},
		{100, 99, 10, 90, 100},
		{100, 2, 10, 0, 10},
	}
	for _, tt := range tests {
		s, e := windowAround(tt.n, tt.sel, tt.size)
		if s != tt.ws || e != tt.we {
			t.Errorf("windowAround(%d,%d,%d)=%d,%d want %d,%d", tt.n, tt.sel, tt.size, s, e, tt.ws, tt.we)
		}
	}
}
