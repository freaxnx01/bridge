package nav

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/agentview"
)

func TestShortRepo(t *testing.T) {
	tests := []struct {
		name, cwd, home, want string
	}{
		{"home prefix", "/home/u/repos/bridge", "/home/u", "~/repos/bridge"},
		{"exact home", "/home/u", "/home/u", "~"},
		{"no home match", "/opt/x", "/home/u", "/opt/x"},
		{"empty home disables", "/home/u/x", "", "/home/u/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortRepo(tt.cwd, tt.home); got != tt.want {
				t.Errorf("shortRepo(%q,%q) = %q, want %q", tt.cwd, tt.home, got, tt.want)
			}
		})
	}
}

func TestBuildAgentRows(t *testing.T) {
	now := time.UnixMilli(100_000)
	sessions := []agentview.Session{
		{Name: "alpha", Status: "busy", Kind: "interactive", CWD: "/home/u/a", StartedAt: time.UnixMilli(40_000)},
		{Name: "zeta", Status: "idle", Kind: "background", CWD: "/opt/z", StartedAt: time.UnixMilli(40_000)},
	}
	rows := buildAgentRows(sessions, "/home/u", now)
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
	if rows[0].dot != "●" || rows[0].repo != "~/a" || rows[0].kind != "interactive" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].dot != "○" || rows[1].repo != "/opt/z" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	if rows[0].age == "" {
		t.Errorf("age should be populated")
	}
}

func TestAgents_PickerKeyOpensScreen(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m2 := out.(Model)
	if m2.screen != screenAgents {
		t.Fatalf("screen = %v, want screenAgents", m2.screen)
	}
	if m2.agentsState != loadPending {
		t.Errorf("agentsState = %v, want loadPending", m2.agentsState)
	}
	if cmd == nil {
		t.Errorf("expected a load cmd")
	}
}

func TestAgents_MsgPopulatesRows(t *testing.T) {
	m := Model{screen: screenAgents, agentsState: loadPending}
	rows := []agentRow{{name: "a"}, {name: "b"}}
	next, _ := m.Update(agentsMsg{rows: rows})
	m2 := next.(Model)
	if len(m2.agents) != 2 || m2.agentsState != loadOK {
		t.Errorf("agents=%d state=%v", len(m2.agents), m2.agentsState)
	}
}

func TestAgents_ErrUnavailableSetsFlag(t *testing.T) {
	m := Model{screen: screenAgents, agentsState: loadPending}
	next, _ := m.Update(agentsErrMsg{unavailable: true})
	m2 := next.(Model)
	if m2.agentsState != loadErr || !m2.agentsUnavailable {
		t.Errorf("state=%v unavailable=%v", m2.agentsState, m2.agentsUnavailable)
	}
}

func TestAgents_EscReturnsToPicker(t *testing.T) {
	m := Model{screen: screenAgents}
	m2, _ := m.updateAgentsKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.screen != screenPicker {
		t.Errorf("screen = %v, want screenPicker", m2.screen)
	}
}

func TestAgents_DownMovesSelection(t *testing.T) {
	m := Model{screen: screenAgents, agents: []agentRow{{}, {}, {}}}
	m2, _ := m.updateAgentsKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m2.agentsSel != 1 {
		t.Errorf("agentsSel = %d, want 1", m2.agentsSel)
	}
}

func TestViewAgents_Golden(t *testing.T) {
	now := time.UnixMilli(3_600_000) // 1h after the sessions below
	sessions := []agentview.Session{
		{Name: "bridge [work]", Status: "busy", Kind: "interactive", CWD: "/home/u/repos/bridge", StartedAt: time.UnixMilli(0)},
		{Name: "notes", Status: "idle", Kind: "background", CWD: "/opt/notes", StartedAt: time.UnixMilli(0)},
	}
	m := Model{
		screen:      screenAgents,
		agentsState: loadOK,
		width:       100,
		height:      30,
		agents:      buildAgentRows(sessions, "/home/u", now),
	}
	assertGolden(t, "agents", stripANSI(m.viewAgents()))
}
