package main

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestResolveCaptureTarget(t *testing.T) {
	repos := []core.Repo{
		{Owner: "freaxnx01", Name: "bridge", Forge: "github"},
		{Owner: "freaxnx01", Name: "agent-os", Forge: "github"},
	}
	// ideas-lab literal
	tg, err := resolveCaptureTarget("ideas-lab", "freaxnx01/ideas-lab", repos)
	if err != nil || !tg.IdeasLab || tg.Owner != "freaxnx01" || tg.Repo != "ideas-lab" {
		t.Fatalf("ideas-lab: %+v err=%v", tg, err)
	}
	// repo by bare name
	tg, err = resolveCaptureTarget("bridge", "", repos)
	if err != nil || tg.IdeasLab || tg.Owner != "freaxnx01" || tg.Repo != "bridge" {
		t.Fatalf("bridge: %+v err=%v", tg, err)
	}
	// explicit owner/name
	tg, err = resolveCaptureTarget("freaxnx01/agent-os", "", repos)
	if err != nil || tg.Owner != "freaxnx01" || tg.Repo != "agent-os" {
		t.Fatalf("owner/name: %+v err=%v", tg, err)
	}
	// unknown
	if _, err := resolveCaptureTarget("nope", "", repos); err == nil {
		t.Errorf("unknown repo should error")
	}
	// ideas-lab requested but unconfigured
	if _, err := resolveCaptureTarget("ideas-lab", "", repos); err == nil {
		t.Errorf("ideas-lab without BRIDGE_IDEAS_LAB_REPO should error")
	}
}

func TestResolveIssueTarget(t *testing.T) {
	repos := []core.Repo{
		{Owner: "freaxnx01", Name: "bridge", Forge: "github"},
		{Owner: "freaxnx01", Name: "agent-os", Forge: "github"},
		{Owner: "freax", Name: "notes", Forge: "forgejo"},
	}
	// bare name, github
	got, err := resolveIssueTarget("bridge", repos)
	if err != nil || got.Owner != "freaxnx01" || got.Repo != "bridge" || got.Forge != "github" {
		t.Fatalf("bridge: %+v err=%v", got, err)
	}
	// bare name, forgejo
	got, err = resolveIssueTarget("notes", repos)
	if err != nil || got.Forge != "forgejo" || got.Owner != "freax" {
		t.Fatalf("notes: %+v err=%v", got, err)
	}
	// explicit owner/name with forge derived from match
	got, err = resolveIssueTarget("freaxnx01/agent-os", repos)
	if err != nil || got.Forge != "github" || got.Repo != "agent-os" {
		t.Fatalf("owner/name: %+v err=%v", got, err)
	}
	// explicit owner/name with no match in discovered repos -> error (we need the forge)
	if _, err := resolveIssueTarget("someone/unknown", repos); err == nil {
		t.Errorf("unknown owner/name should error (forge unknown)")
	}
	// ideas-lab not valid for issues
	if _, err := resolveIssueTarget("ideas-lab", repos); err == nil {
		t.Errorf("ideas-lab target is for ideas only")
	}
	// unknown bare name
	if _, err := resolveIssueTarget("nope", repos); err == nil {
		t.Errorf("unknown repo should error")
	}
}
