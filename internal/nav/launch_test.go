package nav

import (
	"path/filepath"
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
	if !strings.Contains(joined, "bridge-wt-fix") {
		t.Errorf("argv = %v, want canonical session name bridge-wt-fix", argv)
	}
	if strings.Contains(joined, "bridge-fix") && !strings.Contains(joined, "bridge-wt-fix") {
		t.Errorf("argv = %v, must not use legacy session name bridge-fix", argv)
	}
}

func TestRegisterSlotCmd_WritesSlot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slots.json")
	msg := registerSlotCmd(path, core.Slot{ID: "bridge-wt-fix", Repo: "bridge", Worktree: "fix", Agent: "claude"})()
	_ = msg
	slots, err := core.LoadSlots(path)
	if err != nil {
		t.Fatalf("LoadSlots: %v", err)
	}
	if len(slots) != 1 || slots[0].ID != "bridge-wt-fix" || slots[0].Worktree != "fix" {
		t.Fatalf("slot not registered: %+v", slots)
	}
}

func TestLaunchArgvFor_InsideTmux_NestsNotSwitchClient(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	m := initialModel(Config{DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "fix", path: "/r/.worktrees/fix"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	// Issue #114: under tea.ExecProcess nav must nest via `tmux new-session`,
	// NOT emit `sh -c ... switch-client` (which moves the outer client away).
	if argv[0] != "tmux" || argv[1] != "new-session" {
		t.Fatalf("inside tmux, want a tmux new-session launch, got %v", argv)
	}
	if strings.Contains(strings.Join(argv, " "), "switch-client") {
		t.Errorf("argv must not switch-client under tea.ExecProcess: %v", argv)
	}
}

func TestTmuxUnset_RemovesTmuxVars(t *testing.T) {
	got := tmuxUnset([]string{"PATH=/bin", "TMUX=/tmp/x,1,0", "HOME=/h", "TMUX_PANE=%3"})
	for _, e := range got {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			t.Errorf("tmuxUnset left %q", e)
		}
	}
	if len(got) != 2 {
		t.Errorf("tmuxUnset = %v, want PATH+HOME only", got)
	}
}
