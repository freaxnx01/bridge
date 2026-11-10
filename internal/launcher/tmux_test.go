//go:build !windows

package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/agents"
)

func TestTmuxLaunchArgv(t *testing.T) {
	l := &Tmux{}
	got, err := l.LaunchArgv("bridge-main", "/home/me/projects/repos/github/me/public/bridge",
		agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "new-session", "-A", "-s", "bridge-main", "-c",
		"/home/me/projects/repos/github/me/public/bridge", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTmuxAttachArgv(t *testing.T) {
	l := &Tmux{}
	got := l.AttachArgv("bridge-main")
	want := []string{"tmux", "attach-session", "-t", "bridge-main"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTmuxLaunchArgvWithCodeAgent(t *testing.T) {
	l := &Tmux{}
	got, _ := l.LaunchArgv("slot", "/path", agents.AgentSpec{Name: "code", Bin: "code", Args: []string{"."}})
	want := []string{"tmux", "new-session", "-A", "-s", "slot", "-c", "/path", "code", "."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTmuxLaunchArgvNested(t *testing.T) {
	l := &Tmux{}
	got, err := l.LaunchArgvNested("bridge-main", "/r/bridge",
		agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("expected sh -c BODY, got %v", got)
	}
	body := got[2]
	// Must check for an existing session, fall back to detached create, then switch-client.
	for _, want := range []string{
		"tmux has-session -t bridge-main",
		"tmux new-session -d -s bridge-main -c /r/bridge claude",
		"exec tmux switch-client -t bridge-main",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("nested body missing %q\n  body: %s", want, body)
		}
	}
}

func TestTmuxLaunchArgvNestedQuotesValuesWithSpaces(t *testing.T) {
	l := &Tmux{}
	got, _ := l.LaunchArgvNested("slot-x", "/path with spaces",
		agents.AgentSpec{Name: "code", Bin: "code", Args: []string{"."}})
	body := got[2]
	// `-c '/path with spaces'` — single-quoted because of the space.
	if !strings.Contains(body, "-c '/path with spaces'") {
		t.Errorf("expected quoted dir in body: %s", body)
	}
}

func TestTmuxLaunchRejectsEmptySlot(t *testing.T) {
	l := &Tmux{}
	if _, err := l.LaunchArgv("", "/x", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected error on empty slot")
	}
}

func TestTmuxLaunchArgvDetached(t *testing.T) {
	l := &Tmux{}
	got, err := l.LaunchArgvDetached("bridge-wt-x", "/repo/.worktrees/x",
		agents.AgentSpec{Name: "claude", Bin: "claude", Args: []string{"-n", "bridge [x]", "--remote-control"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("want sh -c <body>, got %v", got)
	}
	body := got[2]
	for _, want := range []string{
		"tmux has-session -t bridge-wt-x 2>/dev/null || ",
		"tmux new-session -d -s bridge-wt-x -c /repo/.worktrees/x claude -n 'bridge [x]' --remote-control",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "switch-client") || strings.Contains(body, "attach") {
		t.Errorf("detached launch must not attach/switch: %q", body)
	}
}

func TestTmuxLaunchArgvDetachedValidates(t *testing.T) {
	l := &Tmux{}
	if _, err := l.LaunchArgvDetached("", "/d", agents.AgentSpec{Bin: "claude"}); err == nil {
		t.Error("empty slot: want error")
	}
	if _, err := l.LaunchArgvDetached("s", "", agents.AgentSpec{Bin: "claude"}); err == nil {
		t.Error("empty dir: want error")
	}
	if _, err := l.LaunchArgvDetached("s", "/d", agents.AgentSpec{}); err == nil {
		t.Error("empty bin: want error")
	}
}
