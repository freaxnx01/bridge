package navdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func TestCreateWorktree_CreatesAndIsIdempotent(t *testing.T) {
	dir := gitRepo(t)
	repo := core.Repo{Name: "solo", Path: dir}
	row, err := CreateWorktree(context.Background(), repo, "feat", "#1 [x]")
	if err != nil {
		t.Fatal(err)
	}
	want := DashRow{Worktree: "feat", Branch: "worktree-feat", Path: filepath.Join(dir, ".worktrees", "feat"), DisplayLabel: "#1 [x]"}
	if row != want {
		t.Errorf("row = %+v, want %+v", row, want)
	}
	again, err := CreateWorktree(context.Background(), repo, "feat", "")
	if err != nil || again.Path != want.Path {
		t.Errorf("second call = %+v, %v; want the same path", again, err)
	}
}

func TestCreateWorktree_ExistingPlainDir_FriendlyMessage(t *testing.T) {
	dir := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "taken", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := CreateWorktree(context.Background(), core.Repo{Name: "solo", Path: dir}, "taken", "")
	var wex *worktree.WorktreeExistsError
	if !errors.As(err, &wex) {
		t.Fatalf("err = %v, want WorktreeExistsError", err)
	}
	if msg := WorktreeErrMessage(err); !strings.Contains(msg, `worktree "taken" already exists`) {
		t.Errorf("message = %q", msg)
	}
}

func TestCreateWorktree_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateWorktree(ctx, core.Repo{Path: t.TempDir()}, "x", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRepoChoices_Order(t *testing.T) {
	got := RepoChoices()
	if len(got) != 4 || got[0] != (RepoChoice{"Forgejo · Private", "forgejo", true}) || got[3] != (RepoChoice{"GitHub · Public", "github", false}) {
		t.Errorf("choices = %+v", got)
	}
}
