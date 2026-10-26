package bot

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

// golden compares Render(s) with testdata/<name>.golden: the text, a
// separator line, then the keyboard as indented JSON.
func golden(t *testing.T, name string, s State) {
	t.Helper()
	text, kb := Render(s)
	js, err := json.MarshalIndent(kb, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := text + "\n--- keyboard ---\n" + string(js) + "\n"
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

var bridgeRepo = core.Repo{Name: "bridge", Forge: "github", Owner: "freaxnx01", Visibility: "public", Path: "/r/github/bridge"}

func goldenPicker() State {
	return State{
		MessageID: 10, Epoch: 3, ReposLoaded: true,
		Local: []navdata.RepoEntry{
			{Label: "github/public/bridge", Repo: bridgeRepo},
			{Label: "forgejo/config", Repo: core.Repo{Name: "config", Forge: "forgejo", Owner: "freaxnx01", Path: "/r/git-forgejo/config"}},
			{Label: "github/public/agent-workflow", Repo: core.Repo{Name: "agent-workflow", Forge: "github", Owner: "freaxnx01", Visibility: "public", Path: "/r/github/agent-workflow"}},
		},
		Remote: navdata.RemoteEntries([]forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "obsidian-it", Visibility: "private"}}),
		MRU:    []string{"/r/github/bridge"},
	}
}

func goldenDash() State {
	return State{
		MessageID: 10, Epoch: 5, Screen: ScreenDash, Repo: bridgeRepo,
		Rows: []navdata.DashRow{
			{IsBase: true, Branch: "main", Path: "/r/github/bridge", HasSession: true, SlotID: "bridge", State: "attached"},
			{Worktree: "tg-admin", Branch: "worktree-tg-admin", Path: "/r/github/bridge/.worktrees/tg-admin"},
			{Worktree: "reorg", Branch: "worktree-reorg", Path: "/r/github/bridge/.worktrees/reorg", HasSession: true, SlotID: "bridge-wt-reorg", State: "detached"},
		},
		Status: map[string]RowStatus{
			"/r/github/bridge":                     {Dirty: navdata.Dirty{Clean: true}},
			"/r/github/bridge/.worktrees/tg-admin": {Dirty: navdata.Dirty{Modified: 3, Ahead: 2}},
			"/r/github/bridge/.worktrees/reorg":    {Dirty: navdata.Dirty{Behind: 1, Clean: true}},
		},
		IssuesLoaded: true,
		Issues: []forge.Issue{
			{Number: 312, Title: "fix nav esc"}, {Number: 305, Title: "bot: paging"},
			{Number: 300, Title: "x"}, {Number: 299, Title: "y"},
		},
		Notice: "launched bridge-wt-reorg",
	}
}

func TestRenderGolden(t *testing.T) {
	golden(t, "picker", goldenPicker())

	golden(t, "dash", goldenDash())

	stop := goldenDash()
	stop.Notice, stop.Confirm = "", &Confirm{Kind: ConfirmStop, Index: 2}
	golden(t, "dash_confirm_stop", stop)

	// Review Focus: a repo with zero worktrees still shows its base row.
	golden(t, "dash_zero_worktrees", State{
		MessageID: 10, Epoch: 2, Screen: ScreenDash,
		Repo: core.Repo{Name: "notes", Path: "/r/notes"},
		Rows: []navdata.DashRow{{IsBase: true, Branch: "main", Path: "/r/notes"}},
	})

	// Review Focus: emoji + slash in an issue title.
	golden(t, "issues_emoji", State{
		MessageID: 10, Epoch: 2, Screen: ScreenIssues, Repo: bridgeRepo, IssuesLoaded: true,
		Issues: []forge.Issue{{Number: 7, Title: "🚀 fix/launch: crash"}},
	})

	golden(t, "new_repo_forge", State{
		MessageID: 10, Epoch: 4, ReposLoaded: true, Prompt: PromptNewRepoForge, NewRepoName: "demo",
	})
}

// Review Focus companion: two "bridge" repos on different forges get distinct
// button labels.
func TestPickerButtons_SameNameDisambiguated(t *testing.T) {
	s := State{MessageID: 1, Epoch: 1, ReposLoaded: true, Local: []navdata.RepoEntry{
		{Label: "github/public/bridge", Repo: bridgeRepo},
		{Label: "forgejo/bridge", Repo: core.Repo{Name: "bridge", Forge: "forgejo", Owner: "freaxnx01", Path: "/r/git-forgejo/bridge"}},
	}}
	_, kb := Render(s)
	var texts []string
	for _, row := range kb {
		for _, b := range row {
			if strings.Contains(b.Data, ":r:") {
				texts = append(texts, b.Text)
			}
		}
	}
	if strings.Join(texts, "|") != "bridge · forgejo|bridge · github" {
		t.Errorf("buttons = %v", texts)
	}
}

// Every button of every screen fits Telegram's 64-byte callback limit and is
// valid UTF-8, even with huge indexes and epochs and hostile titles.
func TestRender_CallbackDataFitsAndTextIsValidUTF8(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 2000; i++ {
		many = append(many, navdata.RepoEntry{Label: fmt.Sprintf("github/public/r%04d", i),
			Repo: core.Repo{Name: fmt.Sprintf("r%04d", i), Forge: "github", Path: fmt.Sprintf("/r/%d", i)}})
	}
	var issues []forge.Issue
	for i := 0; i < 500; i++ {
		issues = append(issues, forge.Issue{Number: 100000 + i, Title: strings.Repeat("🚀/ä\n", 40)})
	}
	rows := make([]navdata.DashRow, 300)
	for i := range rows {
		rows[i] = navdata.DashRow{Worktree: strings.Repeat("w", 40) + fmt.Sprint(i), HasSession: true, SlotID: "s", State: "attached"}
	}
	states := []State{
		{Epoch: 1 << 30, ReposLoaded: true, Local: many, Page: 249},
		{Epoch: 1 << 30, Screen: ScreenDash, Repo: bridgeRepo, Rows: rows, Issues: issues, IssuesLoaded: true},
		{Epoch: 1 << 30, Screen: ScreenIssues, Repo: bridgeRepo, Issues: issues, IssuesLoaded: true, IssuePage: 62},
		{Epoch: 1 << 30, Screen: ScreenIssues, Repo: bridgeRepo, Issues: issues, IssuesLoaded: true, Confirm: &Confirm{ConfirmIssue, 499}},
		{Epoch: 1 << 30, Screen: ScreenDash, Repo: bridgeRepo, Rows: rows, Confirm: &Confirm{ConfirmStop, 299}},
	}
	for i, s := range states {
		text, kb := Render(s)
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxTextRunes {
			t.Errorf("state %d: text invalid or too long (%d runes)", i, utf8.RuneCountInString(text))
		}
		for _, row := range kb {
			for _, b := range row {
				if len(b.Data) > MaxCallbackBytes || !utf8.ValidString(b.Text) || strings.Contains(b.Text, "\n") {
					t.Errorf("state %d: bad button %q / %q (%d bytes)", i, b.Text, b.Data, len(b.Data))
				}
			}
		}
	}
}

func TestRender_LoadingAndOverlays(t *testing.T) {
	text, kb := Render(State{Epoch: 1})
	if !strings.Contains(text, "⏳ loading repos…") || len(kb) != 1 {
		t.Errorf("loading picker: %q %v", text, kb)
	}
	text, kb = Render(State{Epoch: 1, ReposLoaded: true, Prompt: PromptFilter})
	if !strings.Contains(text, "✏ Send the filter text") || len(kb) != 1 || kb[0][0].Data != "1:no" {
		t.Errorf("filter prompt: %q %v", text, kb)
	}
	text, _ = Render(State{Epoch: 1, Screen: ScreenDash, Repo: bridgeRepo, Busy: "⏳ starting main…"})
	if !strings.HasSuffix(text, "\n⏳ starting main…") || !strings.Contains(text, "⏳ loading…") {
		t.Errorf("busy dash: %q", text)
	}
}
