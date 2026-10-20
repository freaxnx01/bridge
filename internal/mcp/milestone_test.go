package mcp

import (
	"errors"
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
