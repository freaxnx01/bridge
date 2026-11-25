package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/forge"
)

const mergeHead = "96fae5fd5d9aa4022a3b5a57a69b50510a563891"
const mergeOld = "1111111111111111111111111111111111111111"

var mergeAt = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

// fakeMerger answers the merge path's reads from its fields and records the
// writes it was asked for.
type fakeMerger struct {
	pr       forge.PullRequest
	comments []forge.Comment
	runs     []forge.CheckRun
	statuses []forge.CommitStatus
	files    []forge.PRFile

	prErr    error
	mergeErr error
	delErr   error

	calls []string
}

func (f *fakeMerger) GetPullRequest(_ context.Context, owner, repo string, number int) (forge.PullRequest, error) {
	f.calls = append(f.calls, "get "+ref(owner, repo, number))
	return f.pr, f.prErr
}

func (f *fakeMerger) ListPRComments(_ context.Context, _, _ string, _ int) ([]forge.Comment, error) {
	return f.comments, nil
}

func (f *fakeMerger) ListCheckRuns(_ context.Context, _, _, sha string) ([]forge.CheckRun, error) {
	f.calls = append(f.calls, "checks "+sha)
	return f.runs, nil
}

func (f *fakeMerger) ListCommitStatuses(_ context.Context, _, _, _ string) ([]forge.CommitStatus, error) {
	return f.statuses, nil
}

func (f *fakeMerger) ListPullRequestFiles(_ context.Context, _, _ string, _ int) ([]forge.PRFile, error) {
	return f.files, nil
}

func (f *fakeMerger) MergePullRequest(_ context.Context, owner, repo string, number int, sha, method string) error {
	f.calls = append(f.calls, "merge "+ref(owner, repo, number)+" "+sha+" "+method)
	return f.mergeErr
}

func (f *fakeMerger) DeleteBranch(_ context.Context, owner, repo, branch string) error {
	f.calls = append(f.calls, "delete "+owner+"/"+repo+" "+branch)
	return f.delErr
}

func ref(owner, repo string, number int) string {
	return owner + "/" + repo + "#" + strconv.Itoa(number)
}

func (f *fakeMerger) did(call string) bool {
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (f *fakeMerger) didAny(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// greenMerger is a PR nothing blocks: open, not a draft, green checks, an
// approving review on its current head, head branch in the base repo.
func greenMerger() *fakeMerger {
	return &fakeMerger{
		pr: forge.PullRequest{
			Number: 9, State: "open", HeadSHA: mergeHead, HeadRef: "feat/5-x", BaseRef: "main",
			HeadRepo: "o/a", BaseRepo: "o/a", MergeableState: "clean", ChangedFiles: 1,
			Body: "## Summary\n\nCaps it.\n\nCloses #5",
		},
		comments: []forge.Comment{{
			Author: "github-actions[bot]", Created: mergeAt,
			Body: "<!-- review-pr:" + mergeHead + " -->\n## Automated review — verdict: `approve`",
		}},
		runs:  []forge.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}},
		files: []forge.PRFile{{Path: "a.go", Additions: 3, Deletions: 1}},
	}
}

var mergeReq = api.MergeRequest{Repo: "o/a", Number: 5, PR: 9, HeadSHA: mergeHead}

var mergeBots = []string{"github-actions[bot]", "my-app[bot]"}

func TestMerge_AllGreen_SquashesWithSHAAndDeletesBranch(t *testing.T) {
	m := greenMerger()

	res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err != nil {
		t.Fatal(err)
	}
	if want := "merge o/a#9 " + mergeHead + " squash"; !m.did(want) {
		t.Errorf("calls = %q, want %q", m.calls, want)
	}
	if !m.did("delete o/a feat/5-x") {
		t.Errorf("calls = %q, want the head branch deleted", m.calls)
	}
	if res != (api.MergeResult{Merged: true, BranchDeleted: true}) {
		t.Errorf("result = %+v", res)
	}
}

func TestMerge_HeadMoved_409NoMerge(t *testing.T) {
	m := greenMerger()
	m.pr.HeadSHA = "2222222222222222222222222222222222222222"

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "head moved" {
		t.Fatalf("err = %v, want head moved", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q, a moved head must not be merged", m.calls)
	}
}

func TestMerge_NotOpen_NotMergeable(t *testing.T) {
	m := greenMerger()
	m.pr.State = "closed"

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "PR is not open" {
		t.Fatalf("err = %v, want PR is not open", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q", m.calls)
	}
}

func TestMerge_ReviewOnOlderCommit_NotMergeable(t *testing.T) {
	m := greenMerger()
	m.comments = []forge.Comment{{
		Author: "github-actions[bot]", Created: mergeAt,
		Body: "<!-- review-pr:" + mergeOld + " -->\n## Automated review — verdict: `approve`",
	}}

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "review on older commit" {
		t.Fatalf("err = %v, want review on older commit", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q", m.calls)
	}
}

func TestMerge_ChecksAreReadOnTheHeadSHA(t *testing.T) {
	m := greenMerger()

	if _, err := mergePR(context.Background(), m, mergeBots, mergeReq); err != nil {
		t.Fatal(err)
	}
	if !m.did("checks " + mergeHead) {
		t.Errorf("calls = %q, want the checks read on the head SHA", m.calls)
	}
}

func TestMerge_ForgeRefuses405_ForgeRefused(t *testing.T) {
	m := greenMerger()
	m.mergeErr = &forge.APIError{Status: http.StatusMethodNotAllowed,
		Message: `Required status check "build" is expected.`}

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var refused api.ErrForgeRefused
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want ErrForgeRefused", err)
	}
	if refused.Status != http.StatusMethodNotAllowed || refused.Message != `Required status check "build" is expected.` {
		t.Errorf("refused = %+v", refused)
	}
	if m.didAny("delete ") {
		t.Errorf("calls = %q, a refused merge must not delete the branch", m.calls)
	}
}

func TestMerge_DeleteFails_Still200(t *testing.T) {
	m := greenMerger()
	m.delErr = errors.New("protected branch")

	res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err != nil {
		t.Fatalf("err = %v, a failed branch delete must not fail the merge", err)
	}
	if !res.Merged || res.BranchDeleted || res.BranchError == "" {
		t.Errorf("result = %+v", res)
	}
}

func TestMerge_ForkHead_NoDelete(t *testing.T) {
	m := greenMerger()
	m.pr.HeadRepo = "contributor/a"

	res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err != nil {
		t.Fatal(err)
	}
	if m.didAny("delete ") {
		t.Errorf("calls = %q, a fork's branch is not ours to delete", m.calls)
	}
	if !res.Merged || res.BranchDeleted || res.BranchError == "" {
		t.Errorf("result = %+v", res)
	}
}

func TestMerge_PRReadFails_Error(t *testing.T) {
	m := greenMerger()
	m.prErr = errors.New("boom")

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err == nil {
		t.Fatal("a failed PR read must be an error")
	}
	var notMergeable api.ErrNotMergeable
	var refused api.ErrForgeRefused
	if errors.As(err, &notMergeable) || errors.As(err, &refused) {
		t.Errorf("err = %v, want a plain forge error", err)
	}
}

func TestPipelineBots_Table(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"unset falls back to the Actions bot", "", []string{"github-actions[bot]"}},
		{"blanks and empties are dropped", " a[bot] , ,b[bot] ", []string{"a[bot]", "b[bot]"}},
		{"only separators falls back", " , ", []string{"github-actions[bot]"}},
		{"one entry", "my-app[bot]", []string{"my-app[bot]"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pipelineBots(tc.env)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
