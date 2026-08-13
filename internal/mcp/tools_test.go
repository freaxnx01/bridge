package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/freaxnx01/bridge/internal/audit"
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

// fakePutFile supplies the fileWriter capability (fileReader + PutFile).
type fakePutFile struct {
	*fakeFiles
	htmlURL   string
	putErr    error
	lastPath  string
	lastSHA   string
	lastBody  string
	putCalled bool
}

func (f *fakePutFile) PutFile(_ context.Context, _, _, path string, content []byte, _, sha string) (string, error) {
	f.putCalled = true
	f.lastPath, f.lastSHA, f.lastBody = path, sha, string(content)
	if f.putErr != nil {
		return "", f.putErr
	}
	return f.htmlURL, nil
}

// fakeTree supplies the treeLister capability.
type fakeTree struct {
	entries   []forge.TreeEntry
	truncated bool
	err       error
}

func (f *fakeTree) ListTree(_ context.Context, _, _, _ string, _ bool) ([]forge.TreeEntry, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return f.entries, f.truncated, nil
}

// fakeSearcher supplies the searchCoder capability. It is not embedded in
// fakeFull: GithubClient and ForgejoClient now genuinely diverge on this
// capability (only GitHub implements it), so tests compose it explicitly
// alongside fakeReader rather than pretending every forge has it.
type fakeSearcher struct {
	*fakeReader
	matches    []forge.CodeMatch
	incomplete bool
	err        error
}

func (f *fakeSearcher) SearchCode(_ context.Context, _, _, _ string) ([]forge.CodeMatch, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return f.matches, f.incomplete, nil
}

// fakeIssues supplies the issueCreator capability. forgeName is carried here
// rather than read from fakeReader because embedded structs cannot see one
// another's fields.
type fakeIssues struct {
	forgeName     string
	createCalled  *int
	createErr     error
	closeCalled   *int
	closeErr      error
	updateCalled  *int
	updateErr     error
	labelsCalled  *int
	labelsErr     error
	commentCalled *int
	commentErr    error
}

func (f *fakeIssues) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	if f.createErr != nil {
		return forge.Issue{}, f.createErr
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

func (f *fakeIssues) CloseIssue(_ context.Context, owner, repo string, number int, _ string) (forge.Issue, error) {
	if f.closeCalled != nil {
		*f.closeCalled++
	}
	if f.closeErr != nil {
		return forge.Issue{}, f.closeErr
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: number, State: "closed"}, nil
}

func (f *fakeIssues) UpdateIssue(_ context.Context, owner, repo string, number int, title, _ *string) (forge.Issue, error) {
	if f.updateCalled != nil {
		*f.updateCalled++
	}
	if f.updateErr != nil {
		return forge.Issue{}, f.updateErr
	}
	is := forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: number}
	if title != nil {
		is.Title = *title
	}
	return is, nil
}

func (f *fakeIssues) AddLabels(_ context.Context, _, _ string, _ int, labels []string) ([]string, error) {
	if f.labelsCalled != nil {
		*f.labelsCalled++
	}
	if f.labelsErr != nil {
		return nil, f.labelsErr
	}
	return labels, nil
}

func (f *fakeIssues) CommentIssue(_ context.Context, _, _ string, _ int, body string) (forge.Comment, error) {
	if f.commentCalled != nil {
		*f.commentCalled++
	}
	if f.commentErr != nil {
		return forge.Comment{}, f.commentErr
	}
	return forge.Comment{ID: 7, Body: body}, nil
}

// fakeRepos supplies the repoCreator capability.
type fakeRepos struct {
	forgeName        string
	createRepoCalled *int
	createRepoErr    error
	// visibilityOverride, when non-empty, is returned as the RepoRef's
	// Visibility regardless of the requested private flag — the only way to
	// drive a forge response that disagrees with the request.
	visibilityOverride string
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
	if f.visibilityOverride != "" {
		visibility = f.visibilityOverride
	}
	return forge.RepoRef{Forge: f.forgeName, Owner: "token-owner", Name: name, Visibility: visibility}, nil
}

// fakeRepoUpdater supplies the repoUpdater and topicsSetter capabilities.
type fakeRepoUpdater struct {
	forgeName     string
	repoUpdateErr error
	topicsErr     error
	lastTopics    []string
}

func (f *fakeRepoUpdater) UpdateRepo(_ context.Context, owner, repo string, description *string, private, archived *bool) (forge.RepoRef, error) {
	if f.repoUpdateErr != nil {
		return forge.RepoRef{}, f.repoUpdateErr
	}
	r := forge.RepoRef{Forge: f.forgeName, Owner: owner, Name: repo}
	if description != nil {
		r.Description = *description
	}
	if private != nil && *private {
		r.Visibility = "private"
	} else {
		r.Visibility = "public"
	}
	if archived != nil {
		r.Archived = *archived
	}
	return r, nil
}

func (f *fakeRepoUpdater) SetTopics(_ context.Context, _, _ string, topics []string) ([]string, error) {
	f.lastTopics = topics
	if f.topicsErr != nil {
		return nil, f.topicsErr
	}
	return topics, nil
}

// fakeIssueReader supplies the issueReader capability.
type fakeIssueReader struct {
	issue    forge.Issue
	comments []forge.Comment
	err      error
}

func (f *fakeIssueReader) GetIssue(_ context.Context, _, _ string, _ int) (forge.Issue, []forge.Comment, error) {
	if f.err != nil {
		return forge.Issue{}, nil, f.err
	}
	return f.issue, f.comments, nil
}

// fakeFull has every capability — the GitHub/Forgejo shape.
type fakeFull struct {
	*fakeReader
	*fakeFiles
	*fakeTree
	*fakeIssues
	*fakeRepos
	*fakeRepoUpdater
	*fakePutFile
	*fakeIssueReader
}

// newFakeFull builds a fully capable client. Tests set fields on the embedded
// structs afterwards, e.g. c.repos = … or c.found = false.
func newFakeFull(name string) *fakeFull {
	files := &fakeFiles{}
	return &fakeFull{
		fakeReader:      &fakeReader{name: name},
		fakeFiles:       files,
		fakeTree:        &fakeTree{},
		fakeIssues:      &fakeIssues{forgeName: name},
		fakeRepos:       &fakeRepos{forgeName: name},
		fakeRepoUpdater: &fakeRepoUpdater{forgeName: name},
		fakePutFile:     &fakePutFile{fakeFiles: files},
		fakeIssueReader: &fakeIssueReader{},
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
			name:   "search-only client reports it alongside tier-1 tools",
			client: &fakeSearcher{fakeReader: &fakeReader{name: "github"}},
			want:   []string{"list_repos", "list_issues", "search_code"},
		},
		{
			name: "put_file-capable client reports it alongside read_file",
			client: &struct {
				*fakeReader
				*fakePutFile
			}{fakeReader: &fakeReader{name: "github"}, fakePutFile: &fakePutFile{fakeFiles: &fakeFiles{}}},
			want: []string{"list_repos", "list_issues", "read_file", "put_file"},
		},
		{
			name:   "fully capable client reports every tool",
			client: newFakeFull("github"),
			want: []string{
				"list_repos", "list_issues", "read_file", "put_file", "list_tree", "create_issue", "create_repo",
				"update_repo", "close_issue", "update_issue", "add_labels", "comment_issue", "get_issue",
			},
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

func TestDeps_AuditLogNoopWhenAuditNil(t *testing.T) {
	d := Deps{}
	d.auditLog(audit.Entry{Tool: "close_issue"}) // must not panic
}

func TestDeps_AuditLogAppendsToConfiguredLogger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Audit: logger}

	d.auditLog(audit.Entry{Tool: "close_issue", Outcome: "success"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("audit log line is not valid JSON: %v", err)
	}
	if entry["tool"] != "close_issue" || entry["outcome"] != "success" {
		t.Errorf("audit entry not written correctly: %+v", entry)
	}
}
