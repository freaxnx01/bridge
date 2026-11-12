package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// baseClient satisfies forge.Client only, like ForgejoClient for this purpose.
type baseClient struct{}

func (baseClient) Name() string                                               { return "fake" }
func (baseClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) { return nil, nil }
func (baseClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return nil, nil
}

// labelClient also removes labels and comments, recording the call order.
type labelClient struct {
	baseClient
	calls      []string
	removeErr  error
	commentErr error
}

func (c *labelClient) RemoveLabel(_ context.Context, owner, repo string, _ int, label string) error {
	c.calls = append(c.calls, "remove "+owner+"/"+repo+" "+label)
	return c.removeErr
}

func (c *labelClient) CommentIssue(_ context.Context, _, _ string, _ int, body string) (forge.Comment, error) {
	c.calls = append(c.calls, "comment "+body)
	return forge.Comment{}, c.commentErr
}

func clearRepos() ([]core.Repo, error) {
	return []core.Repo{{Forge: "github", Owner: "anim-bossinfo-ch", Name: "BI-ArchiveUploader"}}, nil
}

var clearReq = api.LaunchRequest{Repo: "anim-bossinfo-ch/BI-ArchiveUploader", Number: 612}

var clearAt = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }

func TestClearEnrichmentLock_Success_RemovesLabelThenComments(t *testing.T) {
	c := &labelClient{}
	var gotForge, gotOwner string
	resolve := func(f, o string) forge.Client { gotForge, gotOwner = f, o; return c }
	if err := clearEnrichmentLock(context.Background(), resolve, clearRepos, clearAt, clearReq); err != nil {
		t.Fatal(err)
	}
	if gotForge != "github" || gotOwner != "anim-bossinfo-ch" {
		t.Fatalf("resolved %s/%s, want the repo owner's client", gotForge, gotOwner)
	}
	want := []string{
		"remove anim-bossinfo-ch/BI-ArchiveUploader enrichment-ongoing",
		"comment 🔓 Enrichment lock cleared from the cockpit at 2026-11-11T12:00:00Z",
	}
	if strings.Join(c.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", c.calls, want)
	}
}

func TestClearEnrichmentLock_Failures(t *testing.T) {
	cases := []struct {
		name   string
		client forge.Client
		repos  func() ([]core.Repo, error)
		want   error
	}{
		{"label removal fails", &labelClient{removeErr: errors.New("403")}, clearRepos, nil},
		{"client cannot remove labels", baseClient{}, clearRepos, api.ErrUnsupportedForge},
		{"no client for the owner", nil, clearRepos, api.ErrNoToken},
		{"repo not cloned", &labelClient{}, func() ([]core.Repo, error) { return nil, nil }, api.ErrUnknownRepo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(string, string) forge.Client { return tc.client }
			err := clearEnrichmentLock(context.Background(), resolve, tc.repos, clearAt, clearReq)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestClearEnrichmentLock_LabelRemovalFails_PostsNoComment(t *testing.T) {
	c := &labelClient{removeErr: errors.New("403")}
	resolve := func(string, string) forge.Client { return c }
	if err := clearEnrichmentLock(context.Background(), resolve, clearRepos, clearAt, clearReq); err == nil {
		t.Fatal("a failed removal must be an error")
	}
	if len(c.calls) != 1 {
		t.Fatalf("calls = %q, want only the failed removal", c.calls)
	}
}

func TestClearEnrichmentLock_CommentFails_StillSucceeds(t *testing.T) {
	c := &labelClient{commentErr: errors.New("boom")}
	resolve := func(string, string) forge.Client { return c }
	if err := clearEnrichmentLock(context.Background(), resolve, clearRepos, clearAt, clearReq); err != nil {
		t.Fatalf("err = %v, the label is the state that matters", err)
	}
}
