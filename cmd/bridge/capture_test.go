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
