package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

func TestHandleListRepos_AggregatesDefaultOwners(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
	fj := newFakeFull("forgejo")
	fj.repos = []forge.RepoRef{{Forge: "forgejo", Owner: "freax", Name: "notes"}}
	clients := map[string]*fakeFull{"github": gh, "forgejo": fj}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 2 {
		t.Fatalf("want 2 repos, got %d: %+v", len(out.Repos), out.Repos)
	}
}

func TestHandleListRepos_ForgeFilterHonoured(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{{Forge: "github", Name: "bridge"}}
	fj := newFakeFull("forgejo")
	fj.repos = []forge.RepoRef{{Forge: "forgejo", Name: "notes"}}
	clients := map[string]*fakeFull{"github": gh, "forgejo": fj}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{Forge: "forgejo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Forge != "forgejo" {
		t.Fatalf("forge filter not honoured: %+v", out.Repos)
	}
}

func TestHandleListRepos_OwnerInputOverridesDefaults(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{{Forge: "github", Owner: "acme", Name: "widget"}}
	clients := map[string]*fakeFull{"github": gh}
	// Default owners intentionally exclude "acme"; explicit input must still query it.
	d := depsWith(clients, []Target{{"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{Forge: "github", Owner: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Owner != "acme" {
		t.Fatalf("owner override not honoured: %+v", out.Repos)
	}
}

func TestHandleListRepos_OwnerWithoutForgeIsRejected(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleListRepos(context.Background(), nil, listReposInput{Owner: "acme"})
	if err == nil {
		t.Fatal("want error when owner is given without forge, got nil")
	}
}

func TestHandleListRepos_UnconfiguredTargetReportsWarningNotSilentDrop(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
	// "forgejo" deliberately absent from clients: ClientFor(forgejo, ...) resolves to nil.
	clients := map[string]*fakeFull{"github": gh}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Forge != "github" {
		t.Fatalf("want the configured github target's repo despite forgejo being unconfigured: %+v", out.Repos)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("want 1 warning for the unconfigured forgejo target, got %+v", out.Warnings)
	}
}

func TestHandleListRepos_PartialFailureReturnsWarningAndSuccessfulResults(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
	fj := newFakeFull("forgejo")
	fj.listErr = errors.New("token expired")
	clients := map[string]*fakeFull{"github": gh, "forgejo": fj}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Forge != "github" {
		t.Fatalf("want the successful github target's repo despite forgejo failing: %+v", out.Repos)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("want 1 warning for the failing forgejo target, got %+v", out.Warnings)
	}
}

func TestHandleReadFile_FoundAndAbsent(t *testing.T) {
	gh := newFakeFull("github")
	gh.file, gh.sha, gh.found = []byte("hello"), "abc", true
	clients := map[string]*fakeFull{"github": gh}
	d := depsWith(clients, nil)

	_, out, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "f.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.Content != "hello" || out.SHA != "abc" {
		t.Fatalf("found file: %+v", out)
	}

	gh.found = false
	gh.file = nil
	_, out, err = d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Fatalf("absent file should have Found=false: %+v", out)
	}
}

func TestHandleReadFile_UnknownForge(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "bogus", Owner: "o", Repo: "r", Path: "f"})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
}

func TestHandleReadFile_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	// A tier-1-only client is the GitLab/ADO shape: resolves fine, has no GetFile.
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleReadFile(context.Background(), nil,
		readFileInput{Forge: "gitlab", Owner: "o", Repo: "r", Path: "f.md"})

	if err == nil {
		t.Fatal("want an error for a client without GetFile, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCrossForgeStatus_DelegatesToBuild(t *testing.T) {
	want := overview.Snapshot{RoadmapErr: "sentinel"}
	d := Deps{BuildOverview: func(_ context.Context) (overview.Snapshot, error) { return want, nil }}
	_, out, err := d.handleCrossForgeStatus(context.Background(), nil, crossForgeStatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.RoadmapErr != "sentinel" {
		t.Fatalf("cross_forge_status did not delegate to BuildOverview: %+v", out)
	}
}

func TestHandleCrossForgeStatus_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	d := Deps{BuildOverview: func(_ context.Context) (overview.Snapshot, error) { return overview.Snapshot{}, sentinel }}
	_, _, err := d.handleCrossForgeStatus(context.Background(), nil, crossForgeStatusInput{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestHandleListRepos_TierOneClientIsFullyUsable(t *testing.T) {
	// The payoff: a client with only tier-1 capabilities still serves list_repos.
	reader := &fakeReader{name: "gitlab", repos: []forge.RepoRef{{Forge: "gitlab", Owner: "acme", Name: "widget"}}}
	d := Deps{
		DefaultOwners: []Target{{"gitlab", "acme"}},
		ClientFor:     func(string, string) ForgeReader { return reader },
	}

	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Name != "widget" {
		t.Fatalf("tier-1 client must serve list_repos: %+v", out)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("a capable tier-1 target must not warn: %+v", out.Warnings)
	}
}
