package cockpit

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

var inboxNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// marker is one run report comment by the only trusted author.
func marker(body string, at time.Time) forge.Comment {
	return forge.Comment{
		ID: 1, Author: "github-actions[bot]", Created: at,
		Body: "## ai-implement run\n\n- outcome: failed\n<!-- ai-run: " + body + " -->",
	}
}

// inboxIssue is an open issue in o/a, titled "T", last updated an hour ago.
func inboxIssue(n int, labels ...string) forge.Issue {
	return forge.Issue{
		Forge: "github", Repo: "o/a", Number: n, Title: "T", URL: "https://x/" + strconv.Itoa(n),
		Labels: labels, Created: inboxNow.Add(-24 * time.Hour), Updated: inboxNow.Add(-time.Hour),
	}
}

// inboxRepo is a wired GitHub repo whose every issue has had its comments read
// at least once: an issue missing from Comments is "never read", which is a
// separate, opt-in case.
func inboxRepo(issues []forge.Issue, runs []forge.WorkflowRun, comments map[int][]forge.Comment) RepoData {
	rd := RepoData{
		Forge: "github", Owner: "o", Name: "a", Path: "/r/a", Wired: true,
		Issues: issues, Runs: runs, Comments: map[int][]forge.Comment{},
	}
	for _, i := range issues {
		rd.Comments[i.Number] = nil
	}
	for n, cs := range comments {
		rd.Comments[n] = cs
	}
	return rd
}

func buildInbox(rd ...RepoData) Inbox { return BuildInbox(InboxInput{Repos: rd, Now: inboxNow}) }

// only returns the single card in whichever bucket holds one, plus that
// bucket's name ("" when every bucket is empty).
func only(t *testing.T, ib Inbox) (string, InboxCard) {
	t.Helper()
	buckets := []struct {
		name  string
		cards []InboxCard
	}{{"cards", ib.Cards}, {"waiting", ib.Waiting}, {"parked", ib.Parked}}
	var gotName string
	var gotCard InboxCard
	for _, b := range buckets {
		if len(b.cards) == 0 {
			continue
		}
		if gotName != "" || len(b.cards) > 1 {
			t.Fatalf("want at most one card overall, got cards=%v waiting=%v parked=%v", ib.Cards, ib.Waiting, ib.Parked)
		}
		gotName, gotCard = b.name, b.cards[0]
	}
	return gotName, gotCard
}

func TestBuildInbox_MappingRows_Table(t *testing.T) {
	retryMarker := `{"v":1,"class":"rate_limit","decision":"stop","attempt":2}`
	failed := []string{dispatch.LabelAIImplement, dispatch.LabelAIFailed}
	redispatch := func(at time.Time) forge.Comment {
		return forge.Comment{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"), Created: at}
	}
	tests := []struct {
		name       string
		labels     []string
		comments   []forge.Comment
		prs        []forge.PullRequest
		wantBucket string
		wantKind   string
		wantReason string
	}{
		{"parked", []string{dispatch.LabelParked}, nil, nil, "parked", CardParked, "parked"},
		{"parked legacy", []string{dispatch.LabelParkedLegacy}, nil, nil, "parked", CardParked, "parked"},
		{"review blocked", []string{dispatch.LabelAIReviewBlocked}, nil, nil, "cards", CardHuman, "review blocked"},
		{"retries exhausted", []string{dispatch.LabelAIImplement},
			[]forge.Comment{marker(retryMarker, inboxNow.Add(-2*time.Hour))}, nil,
			"cards", CardRetry, "rate_limit, retries exhausted"},
		{"paused", []string{dispatch.LabelAIPaused}, nil, nil, "waiting", CardWait, "paused"},
		{"dispatched, no marker", []string{dispatch.LabelAIImplement}, nil, nil, "waiting", CardWait, "no run report yet"},
		{"done, no marker", []string{dispatch.LabelAIDone}, nil, nil, "", "", ""},
		// Rows 15a-15c: the inbox must read a guarded retry exactly as the
		// dispatcher's retry pass does.
		{"re-dispatch pending", failed,
			[]forge.Comment{marker(retryMarker, inboxNow.Add(-2*time.Hour)),
				redispatch(inboxNow.Add(-90 * time.Minute))}, nil,
			"waiting", CardWait, "retry pending"},
		{"re-dispatch never reported", failed,
			[]forge.Comment{marker(retryMarker, inboxNow.Add(-5*time.Hour)),
				redispatch(inboxNow.Add(-4 * time.Hour))}, nil,
			"cards", CardHuman, "re-dispatch never reported"},
		{"open PR", failed,
			[]forge.Comment{marker(retryMarker, inboxNow.Add(-2*time.Hour))},
			[]forge.PullRequest{{Number: 7, Body: "Closes #1"}},
			"cards", CardHuman, "open PR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rd := inboxRepo([]forge.Issue{inboxIssue(1, tc.labels...)}, nil, map[int][]forge.Comment{1: tc.comments})
			rd.PRs = tc.prs
			bucket, card := only(t, buildInbox(rd))
			if bucket != tc.wantBucket {
				t.Fatalf("bucket = %q, want %q (card %+v)", bucket, tc.wantBucket, card)
			}
			if bucket == "" {
				return
			}
			if card.Kind != tc.wantKind || card.Reason != tc.wantReason {
				t.Errorf("kind/reason = %q/%q, want %q/%q", card.Kind, card.Reason, tc.wantKind, tc.wantReason)
			}
		})
	}
}

func TestBuildInbox_NonCandidateOrUnreadComments_Skipped(t *testing.T) {
	notCandidate := inboxRepo([]forge.Issue{inboxIssue(1, "feat")}, nil,
		map[int][]forge.Comment{1: {marker(`{"v":1,"class":"bug"}`, inboxNow)}})

	unread := inboxRepo([]forge.Issue{inboxIssue(1, dispatch.LabelAIFailed)}, nil, nil)
	unread.Comments = map[int][]forge.Comment{}

	unwired := inboxRepo([]forge.Issue{inboxIssue(1, dispatch.LabelAIFailed)}, nil, nil)
	unwired.Wired = false

	forgejo := inboxRepo([]forge.Issue{inboxIssue(1, dispatch.LabelAIFailed)}, nil, nil)
	forgejo.Forge = "forgejo"

	tests := []struct {
		name string
		rd   RepoData
	}{
		{"not a candidate label", notCandidate},
		{"comments never read", unread},
		{"unwired repo", unwired},
		{"non-github forge", forgejo},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ib := buildInbox(tc.rd)
			if len(ib.Cards)+len(ib.Waiting)+len(ib.Parked) != 0 {
				t.Errorf("want every bucket empty, got cards=%v waiting=%v parked=%v", ib.Cards, ib.Waiting, ib.Parked)
			}
		})
	}
}

func TestBuildInbox_CardsUrgency_GroupThenOldest(t *testing.T) {
	at := func(d time.Duration) time.Time { return inboxNow.Add(-d) }
	issueAt := func(n int, updated time.Time, labels ...string) forge.Issue {
		i := inboxIssue(n, labels...)
		i.Updated = updated
		return i
	}
	issues := []forge.Issue{
		issueAt(1, at(time.Hour), dispatch.LabelAIReviewBlocked),
		issueAt(2, at(2*time.Hour), dispatch.LabelAIChecksBlocked),
		issueAt(3, at(time.Minute), dispatch.LabelAIFailed),
		issueAt(4, at(time.Minute), dispatch.LabelAIImplement),
		issueAt(5, at(30*time.Hour), dispatch.LabelAIFailed),
		issueAt(6, at(time.Minute), dispatch.LabelAIImplement, dispatch.LabelAIFailed),
	}
	comments := map[int][]forge.Comment{
		3: {marker(`{"v":1,"class":"task_failure","decision":"stop","attempt":2}`, at(10*time.Hour))},
		4: {marker(`{"v":1,"class":"rate_limit","decision":"stop","attempt":2}`, at(20*time.Hour))},
		// A lost re-dispatch says as little about the run as a missing marker,
		// so it belongs in group 4 rather than ahead of it.
		6: {marker(`{"v":1,"class":"rate_limit","decision":"stop","attempt":2}`, at(5*time.Hour)),
			{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"), Created: at(4 * time.Hour)}},
	}

	ib := buildInbox(inboxRepo(issues, nil, comments))

	want := []int{2, 1, 3, 4, 5, 6}
	var got []int
	for _, c := range ib.Cards {
		got = append(got, c.Number)
	}
	if len(got) != len(want) {
		t.Fatalf("cards = %+v, want %d", ib.Cards, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("card order = %v, want %v", got, want)
		}
	}
	if ib.Cards[4].Reason != "no run marker" {
		t.Errorf("group 4 reason = %q, want %q", ib.Cards[4].Reason, "no run marker")
	}
	if ib.Cards[5].Reason != "re-dispatch never reported" {
		t.Errorf("group 4 reason = %q, want %q", ib.Cards[5].Reason, "re-dispatch never reported")
	}
}

func TestBuildInbox_Since_RunPostedElseUpdated(t *testing.T) {
	posted := inboxNow.Add(-5 * time.Hour)
	withRun := inboxRepo([]forge.Issue{inboxIssue(1, dispatch.LabelAIFailed)}, nil, map[int][]forge.Comment{
		1: {marker(`{"v":1,"class":"bug","pr":42}`, posted)},
	})
	noRun := inboxRepo([]forge.Issue{inboxIssue(2, dispatch.LabelAIFailed)}, nil, nil)

	_, card := only(t, buildInbox(withRun))
	if !card.Since.Equal(posted) {
		t.Errorf("Since = %v, want the marker's Posted %v", card.Since, posted)
	}
	if card.Run == nil || card.Run.PR != 42 {
		t.Errorf("Run = %+v, want PR 42", card.Run)
	}

	_, card = only(t, buildInbox(noRun))
	if !card.Since.Equal(inboxNow.Add(-time.Hour)) {
		t.Errorf("Since = %v, want the issue's Updated", card.Since)
	}
	if card.Run != nil {
		t.Errorf("Run = %+v, want nil without a marker", card.Run)
	}
}

func TestBuildInbox_InFlight_Table(t *testing.T) {
	run := func(event, status, title string) forge.WorkflowRun {
		return forge.WorkflowRun{Event: event, Status: status, DisplayTitle: title, Created: inboxNow}
	}
	tests := []struct {
		name       string
		run        forge.WorkflowRun
		wantBucket string
		wantKind   string
		wantReason string
	}{
		{"in progress for this title", run("issues", "in_progress", "T"), "waiting", CardWait, "running"},
		{"queued for this title", run("issues", "queued", "T"), "waiting", CardWait, "running"},
		{"completed", run("issues", "completed", "T"), "cards", CardRetry, "transient, retries exhausted"},
		{"other event", run("workflow_dispatch", "in_progress", "T"), "cards", CardRetry, "transient, retries exhausted"},
		{"other title", run("issues", "in_progress", "other"), "cards", CardRetry, "transient, retries exhausted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := []forge.Issue{inboxIssue(1, dispatch.LabelAIImplement, dispatch.LabelAIFailed)}
			comments := map[int][]forge.Comment{
				1: {marker(`{"v":1,"class":"transient","decision":"stop","attempt":1}`, inboxNow.Add(-time.Hour))},
			}
			bucket, card := only(t, buildInbox(inboxRepo(issues, []forge.WorkflowRun{tc.run}, comments)))
			if bucket != tc.wantBucket || card.Kind != tc.wantKind || card.Reason != tc.wantReason {
				t.Errorf("got %s/%s/%q, want %s/%s/%q", bucket, card.Kind, card.Reason, tc.wantBucket, tc.wantKind, tc.wantReason)
			}
		})
	}
}

// Two open issues sharing a title both read as running while one run is going:
// a run names its issue only by display title. Documented under Known limits.
func TestBuildInbox_SameTitleTwoIssues_BothInFlight(t *testing.T) {
	issues := []forge.Issue{inboxIssue(1, dispatch.LabelAIImplement), inboxIssue(2, dispatch.LabelAIImplement)}
	runs := []forge.WorkflowRun{{Event: "issues", Status: "in_progress", DisplayTitle: "T", Created: inboxNow}}

	ib := buildInbox(inboxRepo(issues, runs, nil))

	if len(ib.Waiting) != 2 {
		t.Fatalf("Waiting = %+v, want both issues", ib.Waiting)
	}
	for _, c := range ib.Waiting {
		if c.Reason != "running" {
			t.Errorf("#%d reason = %q, want %q", c.Number, c.Reason, "running")
		}
	}
}

func TestBuildInbox_WaitingAndParked_OldestFirst(t *testing.T) {
	updatedAt := func(n int, d time.Duration, labels ...string) forge.Issue {
		i := inboxIssue(n, labels...)
		i.Updated = inboxNow.Add(-d)
		return i
	}
	issues := []forge.Issue{
		updatedAt(1, time.Hour, dispatch.LabelAIPaused),
		updatedAt(2, 5*time.Hour, dispatch.LabelAIPaused),
		updatedAt(3, time.Minute, dispatch.LabelParked),
		updatedAt(4, 9*time.Hour, dispatch.LabelParked),
	}

	ib := buildInbox(inboxRepo(issues, nil, nil))

	if len(ib.Waiting) != 2 || ib.Waiting[0].Number != 2 || ib.Waiting[1].Number != 1 {
		t.Errorf("Waiting = %+v, want #2 then #1", ib.Waiting)
	}
	if len(ib.Parked) != 2 || ib.Parked[0].Number != 4 || ib.Parked[1].Number != 3 {
		t.Errorf("Parked = %+v, want #4 then #3", ib.Parked)
	}
}

func TestBuildInbox_CommentErrs_InErrors(t *testing.T) {
	rd := inboxRepo([]forge.Issue{inboxIssue(5, dispatch.LabelAIFailed)}, nil, nil)
	rd.CommentErrs = map[int]string{5: "boom"}

	ib := buildInbox(rd)

	if got := ib.Errors["comments o/a#5"]; got != "boom" {
		t.Errorf("Errors = %v, want comments o/a#5 -> boom", ib.Errors)
	}
}

func TestBuildInbox_Empty_NonNilSlices(t *testing.T) {
	raw, err := json.Marshal(buildInbox())
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{`"cards":[]`, `"waiting":[]`, `"parked":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s does not contain %s", body, want)
		}
	}
	if strings.Contains(body, `"errors"`) {
		t.Errorf("body %s has an errors key, want it omitted when empty", body)
	}
}

func TestBuildIssueDetail_RunsNewestFirst(t *testing.T) {
	older := marker(`{"v":1,"class":"rate_limit","decision":"retry","attempt":1}`, inboxNow.Add(-5*time.Hour))
	newer := marker(`{"v":1,"class":"task_failure","decision":"stop","attempt":2}`, inboxNow.Add(-time.Hour))
	untrusted := forge.Comment{ID: 9, Author: "someone", Created: inboxNow, Body: newer.Body}
	i := inboxIssue(412, dispatch.LabelAIFailed)
	i.Labels = nil

	d := BuildIssueDetail("o/a", i, []forge.Comment{older, newer, untrusted})

	if len(d.Runs) != 2 {
		t.Fatalf("Runs = %+v, want 2 trusted markers", d.Runs)
	}
	if d.Runs[0].Class != "task_failure" || d.Runs[1].Class != "rate_limit" {
		t.Errorf("Runs order = %q, %q, want newest first", d.Runs[0].Class, d.Runs[1].Class)
	}
	if d.Issue.Repo != "o/a" || d.Issue.Number != 412 {
		t.Errorf("Issue = %+v", d.Issue)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"labels":[]`) {
		t.Errorf("body %s, want nil labels marshalled as []", raw)
	}
}
