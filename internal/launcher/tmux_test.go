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
