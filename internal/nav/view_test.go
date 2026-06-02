package nav

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestView_Picker_ShowsFilterAndRepos(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.localRepos = []repoRow{{label: "github/public/bridge"}}
	out := m.View()
	if !strings.Contains(out, "filter:") {
		t.Errorf("picker view missing filter field")
	}
	if !strings.Contains(out, "bridge") {
		t.Errorf("picker view missing repo row")
	}
}

func TestView_Dash_ShowsCreateRowAndRepoName(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", hasSession: true, agent: "claude", lastAccessed: "1d 2h"}}
	out := m.View()
	if !strings.Contains(out, "fix-x") || !strings.Contains(out, "Create new worktree") {
		t.Errorf("dash view missing rows or create action:\n%s", out)
	}
}

func TestView_Picker_FitsHeightWithLongList(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 20
	m.pickerFocus = focusList
	for i := 0; i < 200; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: fmt.Sprintf("github/public/repo-%03d", i)})
	}
	m.pickerSel = 100
	out := m.View()
	if h := lipgloss.Height(out); h > m.height {
		t.Errorf("picker render height %d exceeds terminal height %d", h, m.height)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("expected a scroll indicator (more) with a long list")
	}
	if !strings.Contains(out, "repo-100") {
		t.Errorf("expected selected row repo-100 within the window")
	}
}

func TestView_Picker_ShowsVersionBottomRight(t *testing.T) {
	m := initialModel(Config{Version: "v9.9.9"})
	m.width, m.height = 100, 30
	m.localRepos = []repoRow{{label: "x"}}
	if out := m.View(); !strings.Contains(out, "v9.9.9") {
		t.Errorf("expected version v9.9.9 in picker view")
	}
}

func TestViewDash_Wide_ShowsDetailPanels(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", path: "/r/fix-x"}}
	m.dashSel = 0
	m.details["/r/fix-x"] = &worktreeDetails{
		branches:      []branchInfo{{name: "worktree-fix-x", current: true}, {name: "main"}},
		commits:       []commitInfo{{sha: "a1b2c3d", subject: "fix login"}},
		status:        []statusFile{{code: " M", path: "internal/nav/view.go"}},
		branchesState: loadOK, commitsState: loadOK, statusState: loadOK,
	}
	out := m.View()
	for _, want := range []string{"Branches", "Recent commits", "Git status", "fix login", "a1b2c3d"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide dash view missing %q\n%s", want, out)
		}
	}
}

func TestViewDash_Narrow_FallsBackToListOnly(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 30 // below dashTwoColMin
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", path: "/r/fix-x"}}
	out := m.View()
	if !strings.Contains(out, "Sessions & Worktrees") {
		t.Errorf("narrow dash should still show the worktree list")
	}
	for _, absent := range []string{"Recent commits", "Git status"} {
		if strings.Contains(out, absent) {
			t.Errorf("narrow dash should not render the %q panel", absent)
		}
	}
}

func TestViewDash_CreateRowSelected_ShowsHint(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.dashSel = 1 // the "+ create" row
	out := m.View()
	if !strings.Contains(out, "select a worktree") {
		t.Errorf("create-row selection should show the select-a-worktree hint\n%s", out)
	}
}

func TestViewDash_Wide_VersionShownOnce(t *testing.T) {
	m := initialModel(Config{Version: "v9.9.9"})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.dashSel = 0
	m.details["/r/fix-x"] = &worktreeDetails{branchesState: loadOK, commitsState: loadOK, statusState: loadOK}
	out := m.View()
	if n := strings.Count(out, "v9.9.9"); n != 1 {
		t.Errorf("version should appear exactly once on the dashboard, got %d\n%s", n, out)
	}
}

func TestViewDash_Wide_ColumnsBottomAligned(t *testing.T) {
	// The right detail column is much taller than the single-worktree left list;
	// the left box must stretch so both columns close their bottom border on the
	// same line (a clean two-column frame).
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.dashSel = 0
	m.details["/r/fix-x"] = &worktreeDetails{
		branches:      []branchInfo{{name: "a"}, {name: "b"}},
		commits:       []commitInfo{{sha: "1", subject: "x"}, {sha: "2", subject: "y"}},
		status:        []statusFile{{code: " M", path: "f"}},
		branchesState: loadOK, commitsState: loadOK, statusState: loadOK,
	}
	out := m.View()
	lines := strings.Split(out, "\n")
	hintIdx := -1
	for i, ln := range lines {
		if strings.Contains(ln, "move") {
			hintIdx = i
			break
		}
	}
	if hintIdx < 1 {
		t.Fatalf("hint line not found in:\n%s", out)
	}
	bottom := lines[hintIdx-1] // bottom-most body line
	if c := strings.Count(bottom, "╰"); c < 2 {
		t.Errorf("expected both columns to close on the bottom body line (2 corners), got %d\nbottom line: %q\nfull:\n%s", c, bottom, out)
	}
}

func TestDirtyView_States(t *testing.T) {
	m := initialModel(Config{})
	tests := []struct {
		name   string
		d      dirtyInfo
		want   []string
		absent []string
	}{
		{"clean in sync", dirtyInfo{clean: true}, []string{"✓ clean"}, []string{"●", "↑", "↓", "upstream"}},
		{"no upstream", dirtyInfo{noUpstream: true, clean: true}, []string{"no upstream"}, []string{"✓ clean", "↑", "↓"}},
		{"modified only", dirtyInfo{modified: 2}, []string{"●2"}, []string{"↑", "↓", "clean"}},
		{"ahead only clean", dirtyInfo{ahead: 1, clean: true}, []string{"↑1"}, []string{"●", "↓", "✓ clean"}},
		{"behind only clean", dirtyInfo{behind: 3, clean: true}, []string{"↓3"}, []string{"●", "↑", "✓ clean"}},
		{"modified ahead behind", dirtyInfo{modified: 2, ahead: 1, behind: 3}, []string{"●2", "↑1", "↓3"}, []string{"clean", "upstream"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.dirtyView(dashRow{dirty: tt.d, dirtyState: loadOK})
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("dirtyView = %q, missing %q", got, w)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("dirtyView = %q, should not contain %q", got, a)
				}
			}
		})
	}
}
