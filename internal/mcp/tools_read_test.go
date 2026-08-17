package mcp

import (
	"context"
	"errors"
	"fmt"
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

func TestHandleListTree_ReturnsEntriesAndTruncated(t *testing.T) {
	gh := newFakeFull("github")
	gh.entries = []forge.TreeEntry{{Path: "README.md", Type: "file", Size: 10, SHA: "abc"}}
	gh.truncated = true
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleListTree(context.Background(), nil,
		listTreeInput{Forge: "github", Owner: "o", Repo: "r", Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Path != "README.md" {
		t.Fatalf("unexpected entries: %+v", out.Entries)
	}
	if !out.Truncated {
		t.Error("want Truncated=true to propagate from the client")
	}
}

func TestHandleListTree_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleListTree(context.Background(), nil,
		listTreeInput{Forge: "bogus", Owner: "o", Repo: "r"})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
}

func TestHandleListTree_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleListTree(context.Background(), nil,
		listTreeInput{Forge: "gitlab", Owner: "o", Repo: "r"})

	if err == nil {
		t.Fatal("want an error for a client without ListTree, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleListTree_ClientErrorPropagatesWrapped(t *testing.T) {
	sentinel := errors.New("boom")
	gh := newFakeFull("github")
	gh.fakeTree.err = sentinel
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleListTree(context.Background(), nil,
		listTreeInput{Forge: "github", Owner: "o", Repo: "r", Path: "src"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the sentinel preserved via %%w, got %v", err)
	}
	if !strings.Contains(err.Error(), "o/r/src") {
		t.Errorf("want the repo path in the wrap, got %v", err)
	}
}

func TestHandleSearchCode_RequiresQuery(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Forge: "github", Owner: "o"})
	if err == nil {
		t.Fatal("want an error when query is empty, got nil")
	}
}

func TestHandleSearchCode_RepoRequiresForgeAndOwner(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x", Repo: "bridge"})
	if err == nil {
		t.Fatal("want an error when repo is given without forge/owner, got nil")
	}
}

func TestHandleSearchCode_ReturnsMatchesAndIncomplete(t *testing.T) {
	gh := &fakeSearcher{
		fakeReader: &fakeReader{name: "github"},
		matches:    []forge.CodeMatch{{Repo: "o/r", Path: "f.go", Line: 3, Text: "func x() {}"}},
		incomplete: true,
	}
	d := Deps{
		DefaultOwners: []Target{{Forge: "github", Owner: "o"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "func x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 1 || out.Matches[0].Path != "f.go" {
		t.Fatalf("unexpected matches: %+v", out.Matches)
	}
	if !out.Incomplete {
		t.Error("want Incomplete=true to propagate from the client")
	}
	if len(out.Warnings) != 0 {
		t.Errorf("a fully successful search must not warn: %v", out.Warnings)
	}
}

func TestHandleSearchCode_UnsupportedForgeWarnsNotErrors(t *testing.T) {
	// forgejo is configured but doesn't implement searchCoder — this must
	// land as a warning (list_git_forges already reports the capability
	// gap), not a silent empty result and not a hard failure.
	fj := &fakeReader{name: "forgejo"}
	d := Deps{
		DefaultOwners: []Target{{Forge: "forgejo", Owner: "freax"}},
		ClientFor:     func(string, string) ForgeReader { return fj },
	}

	_, out, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("want no matches, got %+v", out.Matches)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "does not support search_code") {
		t.Fatalf("want a does-not-support warning naming the gap, got %v", out.Warnings)
	}
}

func TestHandleSearchCode_UnconfiguredTargetWarns(t *testing.T) {
	d := Deps{DefaultOwners: []Target{{Forge: "github", Owner: "o"}}, ClientFor: func(string, string) ForgeReader { return nil }}

	_, out, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "not configured") {
		t.Fatalf("want a not-configured warning, got %v", out.Warnings)
	}
}

func TestHandleSearchCode_RateLimitWarningIsDistinctFromZeroMatches(t *testing.T) {
	gh := &fakeSearcher{fakeReader: &fakeReader{name: "github"}, err: forge.ErrSearchRateLimited}
	d := Deps{
		DefaultOwners: []Target{{Forge: "github", Owner: "o"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("want no matches: %+v", out.Matches)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "rate limited") {
		t.Fatalf("want a rate-limited warning distinguishing this from zero matches, got %v", out.Warnings)
	}
}

func TestHandleSearchCode_PartialFailureReturnsWarningAndSuccessfulResults(t *testing.T) {
	gh := &fakeSearcher{
		fakeReader: &fakeReader{name: "github"},
		matches:    []forge.CodeMatch{{Repo: "o/r", Path: "f.go", Line: 1, Text: "x"}},
	}
	fj := &fakeReader{name: "forgejo"}
	d := Deps{
		DefaultOwners: []Target{{Forge: "github", Owner: "o"}, {Forge: "forgejo", Owner: "freax"}},
		ClientFor: func(forgeName, _ string) ForgeReader {
			if forgeName == "github" {
				return gh
			}
			return fj
		},
	}

	_, out, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 1 {
		t.Fatalf("want the github target's match despite forgejo lacking the capability: %+v", out.Matches)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("want 1 warning for the unsupported forgejo target, got %+v", out.Warnings)
	}
}

func TestHandleSearchCode_OwnerWithoutForgeIsRejected(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleSearchCode(context.Background(), nil, searchCodeInput{Query: "x", Owner: "acme"})
	if err == nil {
		t.Fatal("want error when owner is given without forge, got nil")
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

func TestHandleListIssues_TierOneClientIsFullyUsable(t *testing.T) {
	// The payoff: a client with only tier-1 capabilities still serves
	// list_issues — ListOpenIssues is part of ForgeReader itself, so no
	// capability assertion is needed (GitLab, ADO once wired).
	reader := &fakeReader{name: "gitlab", issues: []forge.Issue{
		{Forge: "gitlab", Repo: "acme/widget", Number: 3, Title: "flaky test"},
	}}
	d := Deps{ClientFor: func(string, string) ForgeReader { return reader }}

	_, out, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "gitlab", Owner: "acme", Repo: "widget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Issues) != 1 || out.Issues[0].Number != 3 {
		t.Fatalf("tier-1 client must serve list_issues: %+v", out)
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
	if len(configured.Capabilities) != 13 {
		t.Errorf("a fully capable client must report 13 tools, got %v", configured.Capabilities)
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
	if len(out.Forges[0].Capabilities) != 5 {
		t.Errorf("want the 5 read tools, got %v", out.Forges[0].Capabilities)
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
	if len(out.Forges[0].Capabilities) != 13 {
		t.Errorf("want all 13 tools, got %v", out.Forges[0].Capabilities)
	}
}

func TestHandleListGitForges_ReportsAllowDestructive(t *testing.T) {
	d := Deps{AllowDestructive: true, ClientFor: func(string, string) ForgeReader { return nil }}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.AllowDestructive {
		t.Error("allow_destructive must reflect Deps.AllowDestructive")
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

func TestHandleGetIssue_ReturnsIssueAndComments(t *testing.T) {
	gh := newFakeFull("github")
	gh.issue = forge.Issue{Forge: "github", Number: 235, Title: "t", Body: "b"}
	gh.comments = []forge.Comment{
		{ID: 1, Author: "alice", Body: "c1"},
		{ID: 2, Author: "bob", Body: "c2"},
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 235})
	if err != nil {
		t.Fatal(err)
	}
	if out.Issue.Number != 235 || out.Issue.Body != "b" {
		t.Errorf("issue: %+v", out.Issue)
	}
	if len(out.Comments) != 2 || out.TotalComments != 2 || out.CommentsTruncated {
		t.Errorf("comments: %+v total=%d truncated=%v", out.Comments, out.TotalComments, out.CommentsTruncated)
	}
}

func TestHandleGetIssue_TruncatesToNewest20(t *testing.T) {
	gh := newFakeFull("github")
	comments := make([]forge.Comment, 25)
	for i := range comments {
		comments[i] = forge.Comment{ID: i, Body: fmt.Sprintf("c%d", i)}
	}
	gh.comments = comments
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Comments) != 20 {
		t.Fatalf("want 20 comments, got %d", len(out.Comments))
	}
	if !out.CommentsTruncated {
		t.Error("want CommentsTruncated=true for a 25-comment thread")
	}
	if out.TotalComments != 25 {
		t.Errorf("want TotalComments=25, got %d", out.TotalComments)
	}
	// Newest 20 kept: comments[5] through comments[24], still in order.
	if out.Comments[0].ID != 5 {
		t.Errorf("want the oldest kept comment to be ID 5, got %d", out.Comments[0].ID)
	}
	if out.Comments[19].ID != 24 {
		t.Errorf("want the newest kept comment to be ID 24, got %d", out.Comments[19].ID)
	}
}

func TestHandleGetIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "bogus", Owner: "o", Repo: "r", IssueNumber: 1})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want a not-configured error, got %v", err)
	}
}

func TestHandleGetIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1})

	if err == nil {
		t.Fatal("want an error for a client without GetIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleGetIssue_ClientErrorPropagates(t *testing.T) {
	gh := newFakeFull("github")
	gh.fakeIssueReader.err = errors.New("404 not found")
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 9999})
	if err == nil {
		t.Fatal("want error to propagate, got nil")
	}
}
