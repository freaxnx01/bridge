package navdata

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

// gitRepo creates a real git repo with one commit on branch main.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir,
			"-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestDashRows_ZeroWorktrees_OnlyBaseRow(t *testing.T) {
	dir := gitRepo(t)
	repo := core.Repo{Name: "solo", Path: dir}
	rows := DashRows(context.Background(), nil, repo, filepath.Join(t.TempDir(), "slots.json"))
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly the base row", rows)
	}
	if !rows[0].IsBase || rows[0].Branch != "main" || rows[0].Path != dir || rows[0].HasSession {
		t.Errorf("base row = %+v", rows[0])
	}
}

func TestDashRows_WorktreeWithLiveSlot(t *testing.T) {
	dir := gitRepo(t)
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-q", "-b", "worktree-feat",
		filepath.Join(dir, ".worktrees", "feat")).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	slots := filepath.Join(t.TempDir(), "slots.json")
	if err := core.UpsertSlot(slots, core.Slot{ID: "solo-wt-feat", Repo: "solo", Worktree: "feat", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	live := func() ([]core.Session, error) {
		return []core.Session{{SlotID: "solo-wt-feat", State: "detached", LastActivity: time.Now()}}, nil
	}
	rows := DashRows(context.Background(), live, core.Repo{Name: "solo", Path: dir}, slots)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want base + feat", rows)
	}
	wt := rows[1]
	if wt.Worktree != "feat" || !wt.HasSession || wt.SlotID != "solo-wt-feat" || wt.Agent != "claude" || wt.State != "detached" {
		t.Errorf("worktree row = %+v", wt)
	}
}

func TestDashRows_NotAGitRepo_StillHasBaseRow(t *testing.T) {
	rows := DashRows(context.Background(), nil, core.Repo{Name: "x", Path: t.TempDir()}, "")
	if len(rows) != 1 || !rows[0].IsBase || rows[0].Branch != "" {
		t.Errorf("rows = %+v, want one base row with empty branch", rows)
	}
}

func TestParseDirtyStatus(t *testing.T) {
	cases := []struct {
		out  string
		want Dirty
	}{
		{"## main...origin/main\n", Dirty{Clean: true}},
		{"## main...origin/main [ahead 2, behind 3]\n M a.go\n?? b\n", Dirty{Modified: 2, Ahead: 2, Behind: 3}},
		{"## feat\n", Dirty{NoUpstream: true, Clean: true}},
	}
	for _, c := range cases {
		if got := ParseDirtyStatus(c.out); got != c.want {
			t.Errorf("ParseDirtyStatus(%q) = %+v, want %+v", c.out, got, c.want)
		}
	}
}

func TestGitDirty_RealRepo(t *testing.T) {
	d, err := GitDirty(context.Background(), gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Clean || !d.NoUpstream {
		t.Errorf("dirty = %+v, want clean + no upstream", d)
	}
}

func TestSessionRows_FallsBackToSlotID(t *testing.T) {
	now := time.Now()
	rows := SessionRows([]core.Session{{SlotID: "x", State: "attached", LastActivity: now}}, nil, now)
	if len(rows) != 1 || rows[0].RepoLabel != "x" || rows[0].LastAccessed != "0m" {
		t.Errorf("rows = %+v", rows)
	}
}
