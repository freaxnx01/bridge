package mcp

import (
	"context"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

// fakeReader implements the tier-1 surface every forge client supports.
// Capability structs below are embedded alongside it to compose a client with
// more than tier-1, so a test can also construct a deliberately partial one.
type fakeReader struct {
	name      string
	repos     []forge.RepoRef
	issues    []forge.Issue
	listErr   error
	issuesErr error
}

func (f *fakeReader) Name() string { return f.name }

func (f *fakeReader) ListRepos(_ context.Context, _ string) ([]forge.RepoRef, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}

func (f *fakeReader) ListOpenIssues(_ context.Context, _, _ string) ([]forge.Issue, error) {
	if f.issuesErr != nil {
		return nil, f.issuesErr
	}
	return f.issues, nil
}

// fakeFiles supplies the fileReader capability.
type fakeFiles struct {
	file  []byte
	sha   string
	found bool
}

func (f *fakeFiles) GetFile(_ context.Context, _, _, _ string) ([]byte, string, bool, error) {
	return f.file, f.sha, f.found, nil
}

// fakeIssues supplies the issueCreator capability. forgeName is carried here
// rather than read from fakeReader because embedded structs cannot see one
// another's fields.
type fakeIssues struct {
	forgeName    string
	createCalled *int
}

func (f *fakeIssues) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

// fakeRepos supplies the repoCreator capability.
type fakeRepos struct {
	forgeName        string
	createRepoCalled *int
	createRepoErr    error
}

func (f *fakeRepos) CreateRepo(_ context.Context, name string, private bool) (forge.RepoRef, error) {
	if f.createRepoCalled != nil {
		*f.createRepoCalled++
	}
	if f.createRepoErr != nil {
		return forge.RepoRef{}, f.createRepoErr
	}
	visibility := "public"
	if private {
		visibility = "private"
	}
	return forge.RepoRef{Forge: f.forgeName, Owner: "token-owner", Name: name, Visibility: visibility}, nil
}

// fakeFull has every capability — the GitHub/Forgejo shape.
type fakeFull struct {
	*fakeReader
	*fakeFiles
	*fakeIssues
	*fakeRepos
}

// newFakeFull builds a fully capable client. Tests set fields on the embedded
// structs afterwards, e.g. c.repos = … or c.found = false.
func newFakeFull(name string) *fakeFull {
	return &fakeFull{
		fakeReader: &fakeReader{name: name},
		fakeFiles:  &fakeFiles{},
		fakeIssues: &fakeIssues{forgeName: name},
		fakeRepos:  &fakeRepos{forgeName: name},
	}
}

func depsWith(clients map[string]*fakeFull, owners []Target) Deps {
	return Deps{
		DefaultOwners: owners,
		ClientFor: func(forgeName, _ string) ForgeReader {
			c, ok := clients[forgeName]
			if !ok {
				return nil
			}
			return c
		},
	}
}

func TestCapabilities_ReportsToolNamesPerCapability(t *testing.T) {
	tests := []struct {
		name   string
		client ForgeReader
		want   []string
	}{
		{
			name:   "nil reader reports nothing",
			client: nil,
			want:   nil,
		},
		{
			name:   "tier-1 only client reports tier-1 tools",
			client: &fakeReader{name: "gitlab"},
			want:   []string{"list_repos", "list_issues"},
		},
		{
			name:   "fully capable client reports every tool",
			client: newFakeFull("github"),
			want:   []string{"list_repos", "list_issues", "read_file", "create_issue", "create_repo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Capabilities(tt.client)
			if len(got) != len(tt.want) {
				t.Fatalf("Capabilities() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("Capabilities()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
