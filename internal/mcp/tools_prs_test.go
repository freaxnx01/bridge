package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// fakePRs implements prLister, prReader and runLister on top of a tier-1
// reader. It is deliberately not part of fakeFull, so the existing
// "fully capable client" Capabilities expectation is unaffected.
type fakePRs struct {
	*fakeReader
	prs       []forge.PullRequest
	pr        forge.PullRequest
	files     []forge.PRFile
	checkRuns []forge.CheckRun
	statuses  []forge.CommitStatus
	runs      []forge.WorkflowRun

	prsErr, getErr, filesErr, checksErr, statusesErr, runsErr error

	gotState, gotSHA, gotBranch, gotHeadSHA string
	gotLimit                                int
	deadline                                time.Duration // remaining time on the ctx the last call received; 0 if none
}

func newFakePRs() *fakePRs { return &fakePRs{fakeReader: &fakeReader{name: "github"}} }

func depsFor(c ForgeReader) Deps {
	return Deps{ClientFor: func(string, string) ForgeReader { return c }}
}

func (f *fakePRs) record(ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
}

func (f *fakePRs) ListPullRequests(ctx context.Context, _, _, state string) ([]forge.PullRequest, error) {
	f.record(ctx)
	f.gotState = state
	return f.prs, f.prsErr
}

func (f *fakePRs) GetPullRequest(ctx context.Context, _, _ string, _ int) (forge.PullRequest, error) {
	f.record(ctx)
	return f.pr, f.getErr
}

func (f *fakePRs) ListPullRequestFiles(ctx context.Context, _, _ string, _ int) ([]forge.PRFile, error) {
	f.record(ctx)
	return f.files, f.filesErr
}

func (f *fakePRs) ListCheckRuns(ctx context.Context, _, _, sha string) ([]forge.CheckRun, error) {
	f.record(ctx)
	f.gotSHA = sha
	return f.checkRuns, f.checksErr
}

func (f *fakePRs) ListCommitStatuses(ctx context.Context, _, _, _ string) ([]forge.CommitStatus, error) {
	f.record(ctx)
	return f.statuses, f.statusesErr
}

func (f *fakePRs) ListWorkflowRuns(ctx context.Context, _, _, branch, headSHA string, limit int) ([]forge.WorkflowRun, error) {
	f.record(ctx)
	f.gotBranch, f.gotHeadSHA, f.gotLimit = branch, headSHA, limit
	return f.runs, f.runsErr
}

func TestHandleListPRs_DefaultsToOpenAndOmitsBody(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{{Number: 90, Title: "t", Body: "Closes #41", Author: "bot", HeadSHA: "abc"}}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if c.gotState != "open" {
		t.Errorf("state: %q, want open", c.gotState)
	}
	if len(out.PRs) != 1 || out.PRs[0].Number != 90 || out.PRs[0].Author != "bot" {
		t.Fatalf("prs: %+v", out.PRs)
	}
	if out.PRs[0].Body != "" {
		t.Errorf("list output must omit body, got %q", out.PRs[0].Body)
	}
}

func TestHandleListPRs_ClosesFilterUsesDispatchRule(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{
		{Number: 1, Body: "Closes #41"},
		{Number: 2, Body: "Closes #410"},
		{Number: 3, Body: "fixes #41 and more"},
		{Number: 4, Body: "refs #41"},
	}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "all", Closes: 41})
	if err != nil {
		t.Fatal(err)
	}
	got := []int{}
	for _, p := range out.PRs {
		got = append(got, p.Number)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("closes=41 kept %v, want [1 3]", got)
	}
	if c.gotState != "all" {
		t.Errorf("state: %q", c.gotState)
	}
}

func TestHandleListPRs_RejectsUnknownState(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "merged"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_RequiresOwnerAndRepo(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "forgejo", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if len(out.PRs) != 0 || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support list_prs" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleListPRs_UnconfiguredForgeErrors(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return nil }}
	_, _, err := d.handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want not-configured error, got %v", err)
	}
}

func TestHandleListPRs_ClientErrorPropagates(t *testing.T) {
	c := newFakePRs()
	c.prsErr = errors.New("502 bad gateway")
	_, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want wrapped client error, got %v", err)
	}
}

func TestHandleListPRs_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	if _, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}
