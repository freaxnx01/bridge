package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestHandleCreateIssue_DraftDoesNotCreate(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createCalled = &calls
	clients := map[string]*fakeFull{"github": gh}
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
	gh := newFakeFull("github")
	gh.createCalled = &calls
	clients := map[string]*fakeFull{"github": gh}
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

func TestHandleCreateIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCreateIssue(context.Background(), nil,
		createIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", Title: "t", Confirm: true})

	if err == nil {
		t.Fatal("want an error for a client without CreateIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCreateRepo_DraftDoesNotCreate(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createRepoCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "freaxnx01", Name: "widget", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Error("want draft=true without confirm")
	}
	// The assertion that matters: an unconfirmed call creates nothing.
	if calls != 0 {
		t.Errorf("an unconfirmed create must not call the forge, got %d calls", calls)
	}
	if out.Owner != "freaxnx01" {
		t.Errorf("the draft echoes the requested owner, got %q", out.Owner)
	}
	if out.Name != "widget" || !out.Private {
		t.Errorf("the draft must echo the request: %+v", out)
	}
}

func TestHandleCreateRepo_ConfirmCreatesAndTakesOwnerFromRepoRef(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createRepoCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	// The requested owner is deliberately NOT the token's account: the fake
	// returns Owner "token-owner", which is what the response must carry.
	_, out, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "requested-owner", Name: "widget", Private: true, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("want exactly 1 create call, got %d", calls)
	}
	if out.Draft {
		t.Error("want draft=false after a confirmed create")
	}
	if out.Owner != "token-owner" {
		t.Errorf("the success response must carry the owner from the RepoRef, not the input, got %q", out.Owner)
	}
	if out.Repo == nil {
		t.Fatal("want the created RepoRef in the response")
	}
	if out.Repo.Visibility != "private" {
		t.Errorf("private must reach the client, got visibility %q", out.Repo.Visibility)
	}
}

func TestHandleCreateRepo_ConfirmDerivesPrivateFromRepoRefNotRequest(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createRepoCalled = &calls
	// The forge downgrades the request: caller asked for private, the
	// returned RepoRef says public. The response must report the forge's
	// answer, not echo the request.
	gh.visibilityOverride = "public"
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "freaxnx01", Name: "widget", Private: true, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Private {
		t.Errorf("Private must be derived from the returned RepoRef, not the request: got true, RepoRef.Visibility=%q", out.Repo.Visibility)
	}
	if out.Repo == nil || out.Repo.Visibility != "public" {
		t.Fatalf("want the forge's visibility preserved in Repo, got %+v", out.Repo)
	}
}

func TestHandleCreateRepo_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "gitlab", Owner: "acme", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleCreateRepo_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "gitlab", Owner: "acme", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CreateRepo, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCreateRepo_RepoExistsGetsDistinctMessage(t *testing.T) {
	gh := newFakeFull("github")
	gh.createRepoErr = forge.ErrRepoExists
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "freaxnx01", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error when the repo exists, got nil")
	}
	if !errors.Is(err, forge.ErrRepoExists) {
		t.Errorf("want ErrRepoExists preserved via %%w, got %v", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want a distinct already-exists message, got %v", err)
	}
	if !strings.Contains(err.Error(), "widget") {
		t.Errorf("the message must name the repo, got %v", err)
	}
}

// Closes the coverage gap the capability-interface review flagged:
// handleReadFile's nil-client branch was tested, handleCreateIssue's was not.
func TestHandleCreateIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCreateIssue(context.Background(), nil,
		createIssueInput{Forge: "gitlab", Owner: "acme", Repo: "widget", Title: "t", Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleCloseIssue_DraftDoesNotCloseOrLogAudit(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.closeCalled = &calls
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, out, err := d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CloseIssue, got %d calls", calls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("draft must not log an audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_ConfirmClosesAndLogsSuccess(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.closeCalled = &calls
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, out, err := d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, StateReason: "completed", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.State != "closed" {
		t.Fatalf("want closed issue in response: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CloseIssue call, got %d", calls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tool":"close_issue"`) || !strings.Contains(string(data), `"outcome":"success"`) {
		t.Errorf("want a success audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_ForgeErrorLogsErrorOutcome(t *testing.T) {
	gh := newFakeFull("github")
	gh.closeErr = errors.New("boom")
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, _, err = d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Confirm: true,
	})
	if err == nil {
		t.Fatal("want an error when CloseIssue fails")
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"outcome":"error"`) {
		t.Errorf("want an error audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCloseIssue(context.Background(), nil,
		closeIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CloseIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCloseIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCloseIssue(context.Background(), nil,
		closeIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleUpdateIssue_RequiresTitleOrBody(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleUpdateIssue(context.Background(), nil,
		updateIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error when both title and body are empty")
	}
	if !strings.Contains(err.Error(), "at least one of title or body") {
		t.Errorf("want a title-or-body-required error, got %v", err)
	}
}

func TestHandleUpdateIssue_DraftDoesNotUpdate(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.updateCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleUpdateIssue(context.Background(), nil, updateIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Title: "t", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call UpdateIssue, got %d calls", calls)
	}
}

func TestHandleUpdateIssue_ConfirmUpdates(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.updateCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleUpdateIssue(context.Background(), nil, updateIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Title: "new title", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.Title != "new title" {
		t.Fatalf("want updated title in response: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 UpdateIssue call, got %d", calls)
	}
}

func TestHandleUpdateIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleUpdateIssue(context.Background(), nil,
		updateIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Title: "t", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without UpdateIssue, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleAddLabels_RequiresNonEmptyLabels(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleAddLabels(context.Background(), nil,
		addLabelsInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error when labels is empty")
	}
	if !strings.Contains(err.Error(), "labels must not be empty") {
		t.Errorf("want a labels-required error, got %v", err)
	}
}

func TestHandleAddLabels_DraftDoesNotAdd(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.labelsCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleAddLabels(context.Background(), nil, addLabelsInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Labels: []string{"bug"}, Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call AddLabels, got %d calls", calls)
	}
}

func TestHandleAddLabels_ConfirmAdds(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.labelsCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleAddLabels(context.Background(), nil, addLabelsInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Labels: []string{"bug", "p1"}, Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if len(out.Labels) != 2 {
		t.Fatalf("want the returned label set: %+v", out.Labels)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 AddLabels call, got %d", calls)
	}
}

func TestHandleAddLabels_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleAddLabels(context.Background(), nil,
		addLabelsInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Labels: []string{"bug"}, Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without AddLabels, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCommentIssue_DraftDoesNotComment(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.commentCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCommentIssue(context.Background(), nil, commentIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Body: "lgtm", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CommentIssue, got %d calls", calls)
	}
}

func TestHandleCommentIssue_ConfirmComments(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.commentCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCommentIssue(context.Background(), nil, commentIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Body: "lgtm", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Comment == nil || out.Comment.Body != "lgtm" {
		t.Fatalf("want the posted comment in response: %+v", out.Comment)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CommentIssue call, got %d", calls)
	}
}

func TestHandleCommentIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCommentIssue(context.Background(), nil,
		commentIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Body: "lgtm", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CommentIssue, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}
