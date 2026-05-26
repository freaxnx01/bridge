package core

import (
	"reflect"
	"testing"
)

func TestParsePaneList(t *testing.T) {
	raw := `bridge|claude
bridge|bash
work-shell|bash
admin|tmux
foo|node
`
	got := ParsePaneList(raw)
	want := map[string][]string{
		"bridge":     {"claude", "bash"},
		"work-shell": {"bash"},
		"admin":      {"tmux"},
		"foo":        {"node"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestParsePaneListSkipsMalformed(t *testing.T) {
	got := ParsePaneList("bridge|claude\nmalformed\n|missing-name\nname-only|\n")
	// "malformed" line skipped; "|missing-name" produces empty key (acceptable
	// — tmux can't actually produce that); "name-only|" produces empty value.
	if v := got["bridge"]; len(v) != 1 || v[0] != "claude" {
		t.Errorf("bridge: %+v", v)
	}
}

func TestSessionRunsKnownAgent(t *testing.T) {
	cases := []struct {
		name string
		cmds []string
		want bool
	}{
		{"claude in foreground", []string{"claude"}, true},
		{"claude alongside bash", []string{"bash", "claude"}, true},
		{"only bash", []string{"bash"}, false},
		{"only tmux/admin", []string{"tmux"}, false},
		{"node (claude wrapper)", []string{"node"}, true},
		{"empty", nil, false},
		{"case insensitive", []string{"Claude"}, true},
		{"trims whitespace", []string{"  claude  "}, true},
	}
	for _, c := range cases {
		if got := SessionRunsKnownAgent(c.cmds); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
