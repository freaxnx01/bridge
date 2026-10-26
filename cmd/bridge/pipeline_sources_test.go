package main

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestCockpitSources_NoClientForOwner_ReportsError(t *testing.T) {
	noClient := func(string, string) forge.Client { return nil }
	src := cockpitSources(noClient)
	r := core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"}
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"issues", func() error { _, err := src.Issues(ctx, r); return err }},
		{"prs", func() error { _, err := src.PRs(ctx, r); return err }},
		{"milestones", func() error { _, err := src.Milestones(ctx, r); return err }},
		{"runs", func() error { _, err := src.Runs(ctx, r); return err }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, errNoForgeClient) {
				t.Fatalf("err = %v, want errNoForgeClient", err)
			}
		})
	}
}

func TestCockpitSources_NonGithubRepo_NoPRsWithoutError(t *testing.T) {
	src := cockpitSources(func(string, string) forge.Client { return nil })
	r := core.Repo{Forge: "forgejo", Owner: "freax", Name: "org"}
	prs, err := src.PRs(context.Background(), r)
	if err != nil || prs != nil {
		t.Fatalf("got %v, %v; want nil, nil", prs, err)
	}
}

func TestCockpitSources_ResolvesPerOwner(t *testing.T) {
	var owners []string
	resolve := func(forgeName, owner string) forge.Client {
		owners = append(owners, forgeName+":"+owner)
		return nil
	}
	src := cockpitSources(resolve)
	if _, err := src.Issues(context.Background(), core.Repo{Forge: "github", Owner: "anim-bossinfo-ch", Name: "x"}); !errors.Is(err, errNoForgeClient) {
		t.Fatalf("err = %v, want errNoForgeClient", err)
	}
	if len(owners) != 1 || owners[0] != "github:anim-bossinfo-ch" {
		t.Fatalf("resolver calls = %v", owners)
	}
}

func TestCachedClients_ResolvesOncePerForgeOwner(t *testing.T) {
	n := 0
	cached := cachedClients(func(string, string) forge.Client { n++; return nil })
	cached("github", "a")
	cached("github", "a")
	cached("github", "b")
	if n != 2 {
		t.Fatalf("resolve calls = %d, want 2", n)
	}
}
