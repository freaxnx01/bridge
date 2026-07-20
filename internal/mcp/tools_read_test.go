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

func TestHandleListIssues_ReturnsIssuesFromConfiguredForge(t *testing.T) {
	gh := newFakeFull("github")
	gh.issues = []forge.Issue{
		{Forge: "github", Repo: "freaxnx01/bridge", Number: 7, Title: "flaky test"},
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "github", Owner: "freaxnx01", Repo: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Issues) != 1 {
		t.Fatalf("want 1 issue, got %+v", out.Issues)
	}
	if out.Issues[0].Number != 7 || out.Issues[0].Title != "flaky test" {
		t.Errorf("unexpected issue: %+v", out.Issues[0])
	}
}

func TestHandleListIssues_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "gitlab", Owner: "acme", Repo: "widget"})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "gitlab") {
		t.Errorf("error must name the forge, got %v", err)
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleListIssues_ClientErrorPropagatesWrapped(t *testing.T) {
	sentinel := errors.New("token expired")
	gh := newFakeFull("github")
	gh.issuesErr = sentinel
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "github", Owner: "freaxnx01", Repo: "bridge"})
	if err == nil {
		t.Fatal("want the client error to propagate, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("want the sentinel preserved via %%w, got %v", err)
	}
	if !strings.Contains(err.Error(), "freaxnx01/bridge") {
		t.Errorf("want the repo path in the wrap, got %v", err)
	}
}

func TestHandleListGitForges_ReportsConfiguredAndUnconfiguredTargets(t *testing.T) {
	// "forgejo" is deliberately absent from clients, so ClientFor returns nil.
	gh := newFakeFull("github")
	d := depsWith(map[string]*fakeFull{"github": gh}, []Target{
		{Forge: "github", Owner: "freaxnx01"},
		{Forge: "forgejo", Owner: "freax"},
	})

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Forges) != 2 {
		t.Fatalf("want 2 targets, got %+v", out.Forges)
	}

	configured := out.Forges[0]
	if configured.Forge != "github" || configured.Owner != "freaxnx01" || !configured.Configured {
		t.Errorf("github target wrong: %+v", configured)
	}
	if configured.Reason != "" {
		t.Errorf("a configured target must carry no reason, got %q", configured.Reason)
	}
	if len(configured.Capabilities) != 5 {
		t.Errorf("a fully capable client must report 5 tools, got %v", configured.Capabilities)
	}

	unconfigured := out.Forges[1]
	if unconfigured.Configured {
		t.Errorf("forgejo must report configured=false: %+v", unconfigured)
	}
	if unconfigured.Reason != "missing token or forge unavailable" {
		t.Errorf("unexpected reason %q", unconfigured.Reason)
	}
	if unconfigured.Capabilities != nil {
		t.Errorf("an unconfigured target must omit capabilities, got %v", unconfigured.Capabilities)
	}
}

func TestHandleListGitForges_TierOneClientReportsOnlyTierOneTools(t *testing.T) {
	d := Deps{
		DefaultOwners: []Target{{Forge: "gitlab", Owner: "acme"}},
		ClientFor:     func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"list_repos", "list_issues"}
	got := out.Forges[0].Capabilities
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestHandleListGitForges_ReadOnlyDropsWriteCapabilities(t *testing.T) {
	gh := newFakeFull("github")
	d := Deps{
		ReadOnly:      true,
		DefaultOwners: []Target{{Forge: "github", Owner: "freaxnx01"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.ReadOnly {
		t.Error("read_only must reflect Deps.ReadOnly")
	}
	for _, c := range out.Forges[0].Capabilities {
		if c == "create_issue" || c == "create_repo" {
			t.Errorf("read-only must not advertise write tools, got %v", out.Forges[0].Capabilities)
		}
	}
	if len(out.Forges[0].Capabilities) != 3 {
		t.Errorf("want the 3 read tools, got %v", out.Forges[0].Capabilities)
	}
}

func TestHandleListGitForges_ReadOnlyFalseKeepsWriteCapabilities(t *testing.T) {
	gh := newFakeFull("github")
	d := Deps{
		DefaultOwners: []Target{{Forge: "github", Owner: "freaxnx01"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.ReadOnly {
		t.Error("read_only must be false when Deps.ReadOnly is false")
	}
	if len(out.Forges[0].Capabilities) != 5 {
		t.Errorf("want all 5 tools, got %v", out.Forges[0].Capabilities)
	}
}

func TestHandleListGitForges_EmptyDefaultOwnersReturnsEmptyListNotNil(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal("empty DefaultOwners is an empty result, not an error:", err)
	}
	if len(out.Forges) != 0 {
		t.Fatalf("want no targets, got %+v", out.Forges)
	}
	// Must be non-nil: a nil slice marshals to JSON null, but the contract
	// says the field is an empty array.
	if out.Forges == nil {
		t.Error("Forges must be an empty slice, not nil, so it marshals to [] not null")
	}
}
