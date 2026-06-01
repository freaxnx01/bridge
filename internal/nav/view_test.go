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
