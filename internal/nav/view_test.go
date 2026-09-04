package nav

import (
	"fmt"
	"os"
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

func TestView_Dash_IssuesFocus_ShowsIssuesPane(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x"}}
	m.issuesState = loadOK
	m.issues = []issueRow{{number: 127, title: "show open forge issues"}}
	m.dashFocus = dashFocusIssues
	out := m.View()
	if !strings.Contains(out, "Open issues") {
		t.Errorf("issues-focused dash should show the Open issues pane:\n%s", out)
	}
	if !strings.Contains(out, "#127") {
		t.Errorf("issues pane should list issue #127:\n%s", out)
	}
}

func TestView_Dash_HeaderShowsOpenCount(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 30
	m.screen = screenDash
	m.repo = core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"}
	m.localRepos = []repoRow{{
		repo:       core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
		issueCount: 3,
		issueState: loadOK,
	}}
	if !strings.Contains(m.View(), "3 open") {
		t.Errorf("dash header should show the open-issue count")
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

// TestView_Picker_RecentSectionHeightBudget pins the exact Repos-list window
// size the picker computes when the Recent section is visible under a
// height-constrained terminal. recentBlock's returned height must equal the
// section's true rendered line count (heading + rows + one blank separator)
// so the Repos-list budget isn't over- or under-reserved by an off-by-one.
func TestView_Picker_RecentSectionHeightBudget(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 20
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Path: "/r/bridge"}},
		{label: "github/public/agent-os", repo: core.Repo{Path: "/r/agent"}},
	}
	m.mruPaths = []string{"/r/bridge", "/r/agent"} // 2-row Recent section
	for i := 0; i < 5; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: fmt.Sprintf("github/public/repo-%03d", i)})
	}
	// 7 repos total (2 recent + 5 plain); with the Recent section's true
	// on-screen footprint (heading + 2 rows + 1 blank = 4 lines) at height 20,
	// the Repos-list budget (maxVisible = 20 - 9 - 4 = 7) exactly fits all 7
	// rows with no truncation indicator.
	out := stripANSI(m.viewPicker())
	if strings.Contains(out, "more") {
		t.Errorf("expected all 7 repos to fit with no truncation indicator:\n%s", out)
	}
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf("repo-%03d", i)
		if !strings.Contains(out, want) {
			t.Errorf("expected row %s to be visible in the Repos list:\n%s", want, out)
		}
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
		{"modified no upstream", dirtyInfo{modified: 1, noUpstream: true}, []string{"●1", "no upstream"}, []string{"✓ clean", "↑", "↓"}},
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

func TestLegend_CoversAuditedGlyphs(t *testing.T) {
	type want struct{ glyph, meaning, group string }
	expected := []want{
		{"●", "session attached (tmux) · agent working (Herdr)", "Session"},
		{"●", "agent blocked — waiting on you (Herdr)", "Session"},
		{"○", "session detached (tmux) · agent idle or done (Herdr)", "Session"},
		{"·", "no session (dashboard row)", "Session"},
		{"●N", "N modified/changed files", "Git status"},
		{"↑N", "N commits ahead of upstream", "Git status"},
		{"↓N", "N commits behind upstream", "Git status"},
		{"✓ clean", "nothing modified/diverged", "Git status"},
		{"⤳ no upstream", "branch has no upstream tracking ref", "Git status"},
		{"?", "dirty-state load error", "Git status"},
		{"⠋", "dirty-state loading (spinner)", "Git status"},
		{"↓ ", "remote-only repo (not cloned; clone on select)", "Rows & selection"},
		{"●N", "open-issue count on a repo row", "Rows & selection"},
		{"▸ ", "selected row (picker list/sessions, create row)", "Rows & selection"},
		{"+", "dashboard action row: create a new worktree", "Rows & selection"},
		{"●N open", "repo open-issue count", "Header"},
		{"✎ <names>", "present note files, e.g. ✎ ideas.md · TODO.md", "Header"},
	}
	if len(legendEntries) != len(expected) {
		t.Fatalf("legendEntries has %d entries, want %d — the legend must document exactly the audited glyph set", len(legendEntries), len(expected))
	}
	for i, e := range legendEntries {
		w := expected[i]
		if e.glyph == "" || e.meaning == "" {
			t.Errorf("entry %d: empty glyph or meaning: %+v", i, e)
		}
		if e.glyph != w.glyph || e.meaning != w.meaning || e.group != w.group {
			t.Errorf("entry %d = {%q,%q,%q}, want {%q,%q,%q}", i, e.glyph, e.meaning, e.group, w.glyph, w.meaning, w.group)
		}
	}
}

// TestLegend_NoPhantomGlyphs guards against a legend entry documenting a glyph
// view.go no longer actually renders. legendEntries is itself defined in
// view.go, so a bare "does this rune appear in view.go" check is vacuous —
// every rune in the table is present by construction (the table literal is
// itself a match). Instead this requires each distinctive rune to appear MORE
// THAN ONCE: once for the legendEntries table row, and at least once more at a
// real render site elsewhere in the file. If a render site is deleted (glyph
// no longer actually drawn) without pruning the corresponding legend entry,
// the count drops to 1 and this test fails.
func TestLegend_NoPhantomGlyphs(t *testing.T) {
	src, err := os.ReadFile("view.go")
	if err != nil {
		t.Fatal(err)
	}
	// Ambiguous as bare substrings — legitimately absent from view.go (the
	// remote-only "↓ " prefix is emitted in data.go) or too generic to prove
	// anything by mere presence.
	skip := map[string]bool{"?": true, "+": true, "↓ ": true}
	distinctive := []string{"●", "○", "·", "↑", "↓", "✓", "⤳", "✎", "▸"}
	for _, e := range legendEntries {
		if skip[e.glyph] {
			continue
		}
		for _, r := range distinctive {
			if !strings.Contains(e.glyph, r) {
				continue
			}
			if n := strings.Count(string(src), r); n < 2 {
				t.Errorf("legend entry %q (%s) uses rune %q, found only %d time(s) in view.go — want it also rendered somewhere beyond the legendEntries table row", e.glyph, e.meaning, r, n)
			}
		}
	}
}

// TestViewLegend_ColumnsAlignByDisplayWidth guards legendRow's contract: pad
// by the glyph's UNSTYLED DISPLAY WIDTH (lipgloss.Width), never by a naive
// rune/byte count of the (possibly styled) glyph string. On a real TTY a
// styled glyph carries ANSI escapes that a naive %-Ns format would count as
// visible width, breaking column alignment — but a colorless test run can't
// reproduce that directly, since Render() is a no-op without a forced color
// profile (see probe below), and forcing one would need the termenv package
// as a new direct import, which this test deliberately avoids.
//
// Instead this exploits a second, TTY-independent place where "display
// width" and "naive length" diverge: East-Asian wide runes. lipgloss.Width
// treats "文" as width 2 (it occupies two terminal columns), while a naive
// rune-count format (e.g. Go's fmt "%-Ns", or len(string) on ASCII) treats
// it as width 1. A revert to length-based padding therefore still pads wide
// glyphs incorrectly even with no ANSI in play, which this test can observe
// deterministically. Verified against a simulated revert
// (fmt.Sprintf("%-14s", e.style.Render(e.glyph))+e.meaning): the old
// approach produced glyph-column widths of 14/15/16 for glyphs "●"/"文"/"文字"
// respectively, while legendRow produces a constant 15 for all three — this
// test asserts that constant-width property directly against the real
// legendRow helper.
func TestViewLegend_ColumnsAlignByDisplayWidth(t *testing.T) {
	entries := []legendEntry{
		{"●", stOk, "narrow ascii glyph", "g"},
		{"文", stBad, "wide cjk glyph", "g"},
		{"文字", stWarn, "two wide cjk glyphs", "g"},
		{"↓N", stAccent, "narrow multi-rune glyph", "g"},
	}
	colWidth := 0
	for _, e := range entries {
		if w := lipgloss.Width(e.glyph); w+1 > colWidth {
			colWidth = w + 1
		}
	}

	var widths []int
	for _, e := range entries {
		row := legendRow(e, colWidth)
		idx := strings.Index(row, e.meaning)
		if idx < 0 {
			t.Fatalf("meaning %q not found in row %q", e.meaning, row)
		}
		widths = append(widths, lipgloss.Width(row[:idx]))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("entry %d (glyph %q) glyph column visual width = %d, want %d (all columns must align) — got widths %v",
				i, entries[i].glyph, w, widths[0], widths)
		}
	}
}

func TestViewLegend_Golden(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 40
	assertGolden(t, "legend", m.viewLegend())
}

func TestView_ShowLegend_ReturnsLegendOverEitherScreen(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 40
	m.showLegend = true
	for _, scr := range []screen{screenPicker, screenDash} {
		m.screen = scr
		out := m.View()
		if !strings.Contains(out, "session attached") {
			t.Errorf("screen %d: View() with showLegend=true should render the legend, got:\n%s", scr, out)
		}
	}
}

func TestView_Picker_HintMentionsLegend(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	if out := m.View(); !strings.Contains(out, "? legend") {
		t.Errorf("picker hint should mention ? legend:\n%s", out)
	}
}

func TestView_Dash_HintMentionsLegend(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	if out := m.View(); !strings.Contains(out, "? legend") {
		t.Errorf("dashboard hint should mention ? legend:\n%s", out)
	}
}

func TestViewPicker_HintLine_AdvertisesCtrlRRefresh(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{{label: "github/public/bridge"}}

	out := m.View()

	if !strings.Contains(out, "r/^r refresh") {
		t.Errorf("picker hint should advertise both r and ^r for refresh; got:\n%s", out)
	}
}

func TestView_Dash_FrameHeightStable_AcrossWorktreeSelection(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{
		{worktree: "sparse", branch: "wt-sparse", path: "/r/sparse"},
		{worktree: "busy", branch: "wt-busy", path: "/r/busy"},
	}
	m.details = map[string]*worktreeDetails{
		"/r/sparse": {
			branches:      []branchInfo{{name: "main", current: true}},
			commits:       []commitInfo{{sha: "abc1234", subject: "init"}},
			branchesState: loadOK,
			commitsState:  loadOK,
			statusState:   loadOK,
		},
		"/r/busy": {
			branches: []branchInfo{
				{name: "main"}, {name: "wt-busy", current: true}, {name: "wt-a"},
				{name: "wt-b"}, {name: "wt-c"}, {name: "wt-d"},
			},
			commits: []commitInfo{
				{sha: "aaa1111", subject: "one"}, {sha: "bbb2222", subject: "two"},
				{sha: "ccc3333", subject: "three"}, {sha: "ddd4444", subject: "four"},
			},
			status: []statusFile{
				{code: "M ", path: "a.go"}, {code: "??", path: "b.go"}, {code: "M ", path: "c.go"},
			},
			branchesState: loadOK,
			commitsState:  loadOK,
			statusState:   loadOK,
		},
	}

	m.dashSel = 0
	sparse := lipgloss.Height(m.View())

	m.dashSel = 1
	busy := lipgloss.Height(m.View())

	if sparse != busy {
		t.Errorf("dashboard frame height changed with selection: sparse-worktree=%d lines, busy-worktree=%d lines", sparse, busy)
	}
}

func TestSessionDot_HerdrStates_MapToDistinctGlyphs(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{"working", stOk.Render("●")},
		{"blocked", stWarn.Render("●")},
		{"idle", stMuted.Render("○")},
		{"done", stMuted.Render("○")},
		{"unknown", stMuted.Render("·")},
		{"attached", stOk.Render("●")},
		{"detached", stMuted.Render("○")},
		{"", stMuted.Render("·")},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if got := sessionDot(tt.state); got != tt.want {
				t.Errorf("sessionDot(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestSessionDot_BlockedIsVisuallyDistinctFromWorking(t *testing.T) {
	// Compare the styles the two states resolve to, not the rendered strings:
	// in a colorless test run lipgloss's Render is a no-op (see the note above
	// TestViewLegend_ColumnsAlignByDisplayWidth), so both "blocked" and
	// "working" would render as the same "●" glyph and the assertion would
	// spuriously fail. The user-visible distinction is the *style* (stOk vs
	// stWarn), which is deterministic without a color profile.
	if stWarn.GetForeground() == stOk.GetForeground() {
		t.Fatal("stWarn and stOk share a foreground; sessionDot cannot make blocked visually distinct")
	}
	if sessionDot("blocked") == sessionDot("working") && stWarn.Render("●") == stOk.Render("●") {
		// Colorless render (no TTY): the rendered strings are equal but the
		// styles differ — the check that matters is above.
		return
	}
	if sessionDot("blocked") == sessionDot("working") {
		t.Error("a blocked agent needs the user; it must not render like a working one")
	}
}
