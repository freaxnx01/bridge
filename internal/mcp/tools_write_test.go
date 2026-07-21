package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

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
