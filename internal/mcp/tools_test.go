package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

// fakeForge is a hand-rolled ForgeClient. Each field is the canned result for
// the corresponding method; nil funcs mean "not expected".
type fakeForge struct {
	name         string
	repos        []forge.RepoRef
	listErr      error
	file         []byte
	sha          string
	found        bool
	created      forge.Issue
	createCalled *int
}

func (f *fakeForge) Name() string { return f.name }
func (f *fakeForge) ListRepos(_ context.Context, _ string) ([]forge.RepoRef, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}
func (f *fakeForge) GetFile(_ context.Context, _, _, _ string) ([]byte, string, bool, error) {
	return f.file, f.sha, f.found, nil
}
func (f *fakeForge) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	return forge.Issue{Forge: f.name, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

func depsWith(clients map[string]*fakeForge, owners []Target) Deps {
	return Deps{
		DefaultOwners: owners,
		ClientFor: func(forgeName, _ string) ForgeClient {
			c, ok := clients[forgeName]
			if !ok {
				return nil
			}
			return c
		},
	}
}

func TestHandleListRepos_AggregatesDefaultOwners(t *testing.T) {
	clients := map[string]*fakeForge{
		"github":  {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}},
		"forgejo": {name: "forgejo", repos: []forge.RepoRef{{Forge: "forgejo", Owner: "freax", Name: "notes"}}},
	}
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
	clients := map[string]*fakeForge{
		"github":  {name: "github", repos: []forge.RepoRef{{Forge: "github", Name: "bridge"}}},
		"forgejo": {name: "forgejo", repos: []forge.RepoRef{{Forge: "forgejo", Name: "notes"}}},
	}
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
	clients := map[string]*fakeForge{
		"github": {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "acme", Name: "widget"}}},
	}
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
	d := depsWith(map[string]*fakeForge{}, nil)
	_, _, err := d.handleListRepos(context.Background(), nil, listReposInput{Owner: "acme"})
	if err == nil {
		t.Fatal("want error when owner is given without forge, got nil")
	}
}

func TestHandleListRepos_UnconfiguredTargetReportsWarningNotSilentDrop(t *testing.T) {
	clients := map[string]*fakeForge{
		"github": {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}},
		// "forgejo" deliberately absent from clients: ClientFor(forgejo, ...) resolves to nil.
	}
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
	clients := map[string]*fakeForge{
		"github":  {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}},
		"forgejo": {name: "forgejo", listErr: errors.New("token expired")},
	}
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
	clients := map[string]*fakeForge{
		"github": {name: "github", file: []byte("hello"), sha: "abc", found: true},
	}
	d := depsWith(clients, nil)

	_, out, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "f.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.Content != "hello" || out.SHA != "abc" {
		t.Fatalf("found file: %+v", out)
	}

	clients["github"].found = false
	clients["github"].file = nil
	_, out, err = d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Fatalf("absent file should have Found=false: %+v", out)
	}
}

func TestHandleReadFile_UnknownForge(t *testing.T) {
	d := depsWith(map[string]*fakeForge{}, nil)
	_, _, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "bogus", Owner: "o", Repo: "r", Path: "f"})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
}

func TestHandleCreateIssue_DraftDoesNotCreate(t *testing.T) {
	calls := 0
	clients := map[string]*fakeForge{"github": {name: "github", createCalled: &calls}}
	d := depsWith(clients, nil)
	_, out, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if out.Issue != nil {
		t.Fatalf("draft must carry no created issue: %+v", out.Issue)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CreateIssue, got %d calls", calls)
	}
	if out.Title != "t" || out.Forge != "github" {
		t.Fatalf("draft must echo resolved fields: %+v", out)
	}
}

func TestHandleCreateIssue_ConfirmCreates(t *testing.T) {
	calls := 0
	clients := map[string]*fakeForge{"github": {name: "github", createCalled: &calls}}
	d := depsWith(clients, nil)
	_, out, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.Number != 42 {
		t.Fatalf("Confirm=true must return created issue: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CreateIssue call, got %d", calls)
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
