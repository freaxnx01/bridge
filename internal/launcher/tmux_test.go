package launcher

import (
	"reflect"
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

func TestTmuxLaunchRejectsEmptySlot(t *testing.T) {
	l := &Tmux{}
	if _, err := l.LaunchArgv("", "/x", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected error on empty slot")
	}
}
