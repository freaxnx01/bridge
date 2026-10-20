package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
)

func testMilestones() []forge.Milestone {
	return []forge.Milestone{
		{Number: 1, Title: "v1", State: "open"},
		{Number: 2, Title: "2026", State: "closed"},
		{Number: 3, Title: "Backlog", State: "open"},
		{Number: 4, Title: "backlog", State: "closed"},
		{Number: 5, Title: "Test-A", State: "open"},
	}
}

func TestResolveMilestone(t *testing.T) {
	tests := []struct {
		name       string
		ms         []forge.Milestone
		ref        string
		wantNumber int
		wantErr    error
		wantInMsg  []string
	}{
		{name: "exact title", ref: "v1", wantNumber: 1},
		{name: "trimmed", ref: " v1 ", wantNumber: 1},
		{name: "case-insensitive unique", ref: "V1", wantNumber: 1},
		{name: "title beats number", ref: "2026", wantNumber: 2},
		{name: "number", ref: "3", wantNumber: 3},
		{name: "exact beats case-insensitive", ref: "Backlog", wantNumber: 3},
		{name: "case-insensitive ambiguous", ref: "BACKLOG", wantErr: ErrMilestoneAmbiguous,
			wantInMsg: []string{"Backlog (#3, open)", "backlog (#4, closed)"}},
		{name: "not found lists titles", ref: "nope", wantErr: ErrMilestoneNotFound, wantInMsg: []string{"v1"}},
		{name: "unknown number", ref: "99", wantErr: ErrMilestoneNotFound},
		{name: "empty ref", ref: "", wantErr: ErrMilestoneNotFound},
		{name: "empty list", ms: []forge.Milestone{}, ref: "v1", wantErr: ErrMilestoneNotFound, wantInMsg: []string{"none"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := tt.ms
			if ms == nil {
				ms = testMilestones()
			}
			got, err := resolveMilestone(ms, tt.ref)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				for _, s := range tt.wantInMsg {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q missing %q", err.Error(), s)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Number != tt.wantNumber {
				t.Errorf("Number = %d, want %d", got.Number, tt.wantNumber)
			}
		})
	}
}

func TestHandleListMilestones(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		client    func() ForgeReader
		wantState string
		wantCalls int
		wantErr   string
	}{
		{name: "default is open", state: "", wantState: "open", wantCalls: 1},
		{name: "all passes through", state: "all", wantState: "all", wantCalls: 1},
		{name: "invalid state", state: "bogus", wantErr: "open, closed or all"},
		{name: "unconfigured forge", client: func() ForgeReader { return nil }, wantErr: "not configured"},
		{name: "no milestone support", client: func() ForgeReader { return &fakeReader{name: "github"} }, wantErr: "does not support milestones"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeFull("github")
			d := depsWith(map[string]*fakeFull{"github": fake}, nil)
			if tt.client != nil {
				d.ClientFor = func(string, string) ForgeReader { return tt.client() }
			}
			_, out, err := d.handleListMilestones(context.Background(), nil, listMilestonesInput{Forge: "github", Owner: "o", Repo: "r", State: tt.state})
			if fake.listCalls != tt.wantCalls {
				t.Errorf("list calls = %d, want %d", fake.listCalls, tt.wantCalls)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fake.listState != tt.wantState {
				t.Errorf("state = %q, want %q", fake.listState, tt.wantState)
			}
			if len(out.Milestones) != len(testMilestones()) {
				t.Errorf("milestones = %v", out.Milestones)
			}
		})
	}
}

func TestCapabilities_RealClients_ReportMilestoneTools(t *testing.T) {
	clients := map[string]ForgeReader{
		"github":  forge.NewGithubClient("t", ""),
		"forgejo": forge.NewForgejoClient("t", ""),
	}
	for name, c := range clients {
		t.Run(name, func(t *testing.T) {
			got := Capabilities(c)
			for _, want := range []string{"list_milestones", "set_milestone"} {
				if !slices.Contains(got, want) {
					t.Errorf("Capabilities = %v, missing %q", got, want)
				}
			}
		})
	}
}

func TestHandleSetMilestone(t *testing.T) {
	tests := []struct {
		name      string
		in        setMilestoneInput
		setErr    error
		wantErr   string
		wantIs    error
		wantDraft bool
		wantList  int
		wantSets  int
		wantTo    int
		wantClear bool
		wantMS    int
	}{
		{name: "both milestone and clear", in: setMilestoneInput{Milestone: "v1", Clear: true, Confirm: true}, wantErr: "exactly one of milestone or clear is required"},
		{name: "neither", in: setMilestoneInput{Confirm: true}, wantErr: "exactly one of milestone or clear is required"},
		{name: "draft assign resolves without writing", in: setMilestoneInput{Milestone: "v1"}, wantDraft: true, wantList: 1, wantMS: 1},
		{name: "draft clear is network-free", in: setMilestoneInput{Clear: true}, wantDraft: true, wantClear: true},
		{name: "confirm assign by number", in: setMilestoneInput{Milestone: "3", Confirm: true}, wantList: 1, wantSets: 1, wantTo: 3, wantMS: 3},
		{name: "confirm clear sends zero", in: setMilestoneInput{Clear: true, Confirm: true}, wantSets: 1, wantTo: 0, wantClear: true},
		{name: "unknown title", in: setMilestoneInput{Milestone: "nope", Confirm: true}, wantIs: ErrMilestoneNotFound, wantList: 1},
		{name: "setter error is wrapped", in: setMilestoneInput{Milestone: "v1", Confirm: true}, setErr: errors.New("boom"), wantErr: "boom", wantList: 1, wantSets: 1, wantTo: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeFull("github")
			fake.setErr = tt.setErr
			d := depsWith(map[string]*fakeFull{"github": fake}, nil)
			tt.in.Forge, tt.in.Owner, tt.in.Repo, tt.in.IssueNumber = "github", "o", "r", 7

			_, out, err := d.handleSetMilestone(context.Background(), nil, tt.in)

			if fake.listCalls != tt.wantList || fake.setCalls != tt.wantSets {
				t.Errorf("list/set calls = %d/%d, want %d/%d", fake.listCalls, fake.setCalls, tt.wantList, tt.wantSets)
			}
			if tt.wantSets > 0 && (fake.setNumber != 7 || fake.setTo != tt.wantTo) {
				t.Errorf("setter got issue %d milestone %d, want 7 / %d", fake.setNumber, fake.setTo, tt.wantTo)
			}
			if tt.wantErr != "" || tt.wantIs != nil {
				assertSetMilestoneErr(t, err, tt.wantErr, tt.wantIs)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertSetMilestoneOut(t, out, tt.wantDraft, tt.wantClear, tt.wantMS)
		})
	}
}

func assertSetMilestoneErr(t *testing.T, err error, wantMsg string, wantIs error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error")
	}
	if wantMsg != "" && !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("err = %v, want containing %q", err, wantMsg)
	}
	if wantIs != nil && !errors.Is(err, wantIs) {
		t.Errorf("err = %v, want %v", err, wantIs)
	}
}

func assertSetMilestoneOut(t *testing.T, out setMilestoneOutput, draft, clear bool, milestone int) {
	t.Helper()
	if out.Draft != draft || out.Clear != clear {
		t.Errorf("draft/clear = %v/%v, want %v/%v", out.Draft, out.Clear, draft, clear)
	}
	if milestone == 0 && out.Milestone != nil {
		t.Errorf("unexpected milestone %+v", out.Milestone)
	}
	if milestone != 0 && (out.Milestone == nil || out.Milestone.Number != milestone) {
		t.Errorf("milestone = %+v, want number %d", out.Milestone, milestone)
	}
	if draft == (out.Issue != nil) {
		t.Errorf("issue = %+v with draft=%v", out.Issue, draft)
	}
}

func TestHandleSetMilestone_AuditsOutcome(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setErr  error
		outcome string
	}{
		{name: "success", outcome: `"outcome":"success"`},
		{name: "error", setErr: errors.New("boom"), outcome: `"outcome":"error"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeFull("github")
			fake.setErr = tt.setErr
			path := filepath.Join(t.TempDir(), "audit.jsonl")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			d := depsWith(map[string]*fakeFull{"github": fake}, nil)
			d.Audit = logger

			_, _, _ = d.handleSetMilestone(context.Background(), nil, setMilestoneInput{
				Forge: "github", Owner: "o", Repo: "r", IssueNumber: 7, Milestone: "v1", Confirm: true,
			})

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"tool":"set_milestone"`) || !strings.Contains(string(data), tt.outcome) {
				t.Errorf("audit = %q, want set_milestone with %s", data, tt.outcome)
			}
		})
	}
}

func TestHandleListGitForges_ReadOnlyHidesSetMilestone(t *testing.T) {
	d := Deps{
		ReadOnly:      true,
		DefaultOwners: []Target{{Forge: "github", Owner: "o"}},
		ClientFor:     func(string, string) ForgeReader { return newFakeFull("github") },
	}
	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	caps := out.Forges[0].Capabilities
	if slices.Contains(caps, "set_milestone") || !slices.Contains(caps, "list_milestones") {
		t.Errorf("read-only capabilities = %v, want list_milestones without set_milestone", caps)
	}
}
