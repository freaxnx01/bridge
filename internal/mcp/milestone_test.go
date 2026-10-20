package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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
			for _, want := range []string{"list_milestones"} {
				if !slices.Contains(got, want) {
					t.Errorf("Capabilities = %v, missing %q", got, want)
				}
			}
		})
	}
}
