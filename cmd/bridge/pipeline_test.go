package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/core"
)

func TestResolveLaunchName_Cases(t *testing.T) {
	repos := []core.Repo{
		{Owner: "a", Name: "bridge"},
		{Owner: "a", Name: "dup"},
		{Owner: "b", Name: "dup"},
	}
	for _, tc := range []struct {
		name, repo string
		want       string
		wantErr    error
		anyErr     bool
	}{
		{name: "unique", repo: "a/bridge", want: "bridge"},
		{name: "shared basename", repo: "a/dup", wantErr: api.ErrAmbiguousRepo},
		{name: "unknown", repo: "a/nope", anyErr: true},
		{name: "owner mismatch", repo: "z/bridge", anyErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLaunchName(repos, tc.repo)
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			case tc.anyErr && err == nil:
				t.Fatal("want error")
			case !tc.anyErr && tc.wantErr == nil && (err != nil || got != tc.want):
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestLaunchArgs_Enrich_ExactArgv(t *testing.T) {
	got := launchArgs("bridge", "enrich", 7, "/enrich 7 --headless")
	want := []string{"launch", "bridge", "-w", "enrich-7", "--agent", "claude", "--rc", "--rc-wait", "15s", "--json", "--prompt", "/enrich 7 --headless"}
	if !slices.Equal(got, want) {
		t.Errorf("argv = %v", got)
	}
}

func TestParseLaunchResult_SnakeCaseMapped(t *testing.T) {
	got, err := parseLaunchResult([]byte(`{"slot":"s","already_running":true,"rc_url":"https://claude.ai/code/session_A","dir":"/x"}`))
	if err != nil || got != (api.LaunchResult{Slot: "s", AlreadyRunning: true, RCURL: "https://claude.ai/code/session_A"}) {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := parseLaunchResult([]byte(`{}`)); err == nil {
		t.Error("want error for missing slot")
	}
}
