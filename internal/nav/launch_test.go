package nav

import (
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestLaunchArgvFor_AttachExisting(t *testing.T) {
	m := initialModel(Config{})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "fix", path: "/r/.worktrees/fix", hasSession: true, slotID: "s-fix"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	want := []string{"tmux", "attach-session", "-t", "s-fix"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}
}

func TestLaunchArgvFor_NoSession_LaunchesAgentInWorktree(t *testing.T) {
	t.Setenv("TMUX", "") // deterministic: force the non-nested LaunchArgv path
	m := initialModel(Config{DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "fix", path: "/r/.worktrees/fix"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	if argv[0] != "tmux" || argv[1] != "new-session" {
		t.Fatalf("argv = %v, want a tmux new-session launch", argv)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "/r/.worktrees/fix") || !strings.Contains(joined, "claude") {
		t.Errorf("argv = %v, want dir + claude", argv)
	}
}
