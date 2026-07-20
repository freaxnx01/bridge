package mcp

import (
	"context"
	"strings"
	"testing"
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
