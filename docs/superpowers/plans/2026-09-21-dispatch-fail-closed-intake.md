# Fail Closed at Intake and Dispatch — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop unenriched, capture-created issues from being dispatch-eligible — by stamping `needs-enrichment` at every intake path, and by rejecting empty-body issues in `Eligible()` regardless of labels.

**Architecture:** Two independent mechanisms. Intake: `CreateIssue` grows a `labels []string` parameter so the label lands in the same atomic POST that creates the issue, with a per-client `ensureLabel` helper that creates the label in repos that don't define it. Dispatch: `Eligible()` rejects a blank body, which first requires `ListOpenIssues` to actually map `Body` (it never has) — and that in turn requires clearing `Body` at the two outward-facing boundaries so issue bodies don't leak into summary listings.

**Tech Stack:** Go (stdlib only). `net/http` + `httptest` for client tests, hand-rolled fakes for handler tests. No new dependency — do not add testify/mockery.

**Spec:** [`docs/superpowers/specs/2026-09-21-dispatch-fail-closed-intake-design.md`](../specs/2026-09-21-dispatch-fail-closed-intake-design.md)

## Global Constraints

- Go stack overlay rules apply: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green after **every** task.
- **No new test dependency.** Hand-rolled fakes and `httptest` only — no testify, mockery, gomock.
- Table-driven tests with `t.Run` subtests are the default shape; name tests `TestFunc_StateUnderTest_ExpectedBehavior`.
- Never `panic`; return wrapped errors with `%w`. Never silence an error with `_ =` (a deferred best-effort `Close` with an explanatory comment is the existing exception).
- No `//nolint`. No commented-out code. No debug `fmt.Println`.
- The label name comes from the existing constant `dispatch.LabelNeedsEnrichment` (`internal/dispatch/eligible.go:13`) — do not hardcode the string `"needs-enrichment"` in new code. `internal/dispatch` imports only `internal/forge` and `internal/store`, so importing it from `internal/capture` and `internal/mcp` creates no cycle.
- Commit after every task. Conventional Commits, scope `dispatch`, `forge`, `capture`, `mcp` or `api` as appropriate.
- Each task must leave the tree compiling and the full suite green — Task 4 changes a shared signature, so its call-site updates are part of that same task.

---

### Task 1: `Eligible()` rejects an empty body

**Files:**
- Modify: `internal/dispatch/eligible.go:89-109` (the `Eligible` function)
- Test: `internal/dispatch/eligible_test.go:72-110` (the `TestEligible` table)
- Test: `cmd/bridge/dispatch_test.go:51-68` (`TestCollectCandidatesSkipsNonGithubAndIneligible`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `Eligible(i forge.Issue, activeMilestone string, prs []forge.PullRequest) (bool, string)` — unchanged signature, new first-checked rule returning reason string `"empty body"`.

**Important — existing fixtures must gain a body.** Every case in the current `TestEligible` table uses a `forge.Issue` with no `Body`, so they would all start returning `"empty body"`. Updating those fixtures is **not** modifying tests to go green: the contract changed, and a dispatchable issue now requires a body by definition. Each fixture gets a realistic body so it continues to test the rule it was written for.

- [ ] **Step 1: Write the failing test**

In `internal/dispatch/eligible_test.go`, replace the `base` fixture and add the new cases. `base` gains a body; the three new cases cover blank, whitespace-only, and the real-world `#31` shape (empty body *and* no labels at all — the case that motivated this issue):

```go
func TestEligible(t *testing.T) {
	base := forge.Issue{Number: 41, Body: "a real description", Labels: []string{"feat"}, Milestone: "v2 search"}

	cases := []struct {
		name       string
		issue      forge.Issue
		milestone  string
		prs        []forge.PullRequest
		wantOK     bool
		wantReason string
	}{
		{"happy path", base, "v2 search", nil, true, ""},
		{"no active milestone dispatches anyway",
			forge.Issue{Number: 41, Body: "d", Milestone: ""}, "", nil, true, ""},
		{"empty body",
			forge.Issue{Number: 41, Body: "", Labels: []string{"feat"}}, "", nil,
			false, "empty body"},
		{"whitespace-only body",
			forge.Issue{Number: 41, Body: "  \n\t ", Labels: []string{"feat"}}, "", nil,
			false, "empty body"},
		{"unlabeled capture issue with no body is rejected on the body, not the labels",
			forge.Issue{Number: 31, Body: ""}, "", nil,
			false, "empty body"},
		{"not enriched",
			forge.Issue{Number: 41, Body: "d", Labels: []string{"needs-enrichment"}}, "", nil,
			false, "needs-enrichment"},
		{"parked",
			forge.Issue{Number: 41, Body: "d", Labels: []string{"🧊 parked"}}, "", nil,
			false, "parked"},
		{"attempt budget spent",
			forge.Issue{Number: 41, Body: "d", Labels: []string{"attempt:2"}}, "", nil,
			false, "attempts exhausted"},
		{"has open PR", base, "v2 search",
			[]forge.PullRequest{{Number: 90, Body: "Closes #41"}},
			false, "open PR"},
		{"already dispatched, no open PR",
			forge.Issue{Number: 41, Body: "d", Labels: []string{"ai-implement"}}, "", nil,
			false, "already dispatched"},
		{"outside active milestone",
			forge.Issue{Number: 41, Body: "d", Milestone: "backlog"}, "v2 search", nil,
			false, "outside active milestone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := Eligible(c.issue, c.milestone, c.prs)
			if ok != c.wantOK || reason != c.wantReason {
				t.Errorf("got (%v, %q), want (%v, %q)", ok, reason, c.wantOK, c.wantReason)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch -run TestEligible -v`
Expected: FAIL — the three new cases report `got (true, "")`, want `(false, "empty body")`.

- [ ] **Step 3: Write the minimal implementation**

In `internal/dispatch/eligible.go`, add the check as the **first** rule in `Eligible`, above the `needs-enrichment` check:

```go
func Eligible(i forge.Issue, activeMilestone string, prs []forge.PullRequest) (bool, string) {
	// Checked before the label rules on purpose: an issue with no body is unfit
	// for dispatch for a reason that has nothing to do with its labels, and an
	// intake path that forgot to stamp needs-enrichment must still be caught.
	if strings.TrimSpace(i.Body) == "" {
		return false, "empty body"
	}
	if hasLabel(i.Labels, LabelNeedsEnrichment) {
		return false, "needs-enrichment"
	}
	// ... rest unchanged
```

`strings` is already imported (`eligible.go:6`).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/dispatch -run TestEligible -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Fix the `collectCandidates` fixture and add its case**

`cmd/bridge/dispatch_test.go` fails now for the same fixture reason. Update it and add the empty-body candidate:

```go
func TestCollectCandidatesSkipsNonGithubAndIneligible(t *testing.T) {
	repos := []repoInput{
		{Forge: "github", Owner: "o", Name: "quotes",
			Issues: []forge.Issue{
				{Number: 41, Body: "a real description", Labels: []string{"feat"}},
				{Number: 42, Body: "d", Labels: []string{"needs-enrichment"}},
				{Number: 31, Body: ""}, // capture-created, no body, no labels
			}},
		{Forge: "forgejo", Owner: "f", Name: "notes",
			Issues: []forge.Issue{{Number: 1, Body: "d", Labels: []string{"feat"}}}},
	}

	got := collectCandidates(repos)
	if len(got) != 1 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	if got[0].Issue.Number != 41 || got[0].Repo != "quotes" {
		t.Errorf("got %+v", got[0])
	}
}
```

- [ ] **Step 6: Run the full suite**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: no `gofmt` output, vet clean, all packages PASS. If another package's fixtures also relied on a body-less issue being eligible, fix those fixtures the same way — add a body, don't weaken the rule.

- [ ] **Step 7: Commit**

```bash
git add internal/dispatch/eligible.go internal/dispatch/eligible_test.go cmd/bridge/dispatch_test.go
git commit -m "fix(dispatch): reject empty-body issues regardless of labels (#303)"
```

---

### Task 2: `ListOpenIssues` maps `Body`

**Files:**
- Modify: `internal/forge/github.go:362-379` (`ghIssue`), `internal/forge/github.go:525-556` (`ListOpenIssues`)
- Modify: `internal/forge/forgejo.go:522-531` (`fjIssue`), `internal/forge/forgejo.go:533-554` (`ListOpenIssues`)
- Test: `internal/forge/github_test.go` (`TestGithubListIssues`), `internal/forge/forgejo_test.go` (the Forgejo list-issues test)

**Interfaces:**
- Consumes: the `"empty body"` rule from Task 1 — without this task that rule rejects every issue, because `Body` is never populated on a listing.
- Produces: `forge.Issue.Body` populated by both `ListOpenIssues` implementations. Consumed by `Eligible` (Task 1) and cleared at the boundaries (Task 3).

- [ ] **Step 1: Write the failing test**

In `internal/forge/github_test.go`, extend `TestGithubListIssues` — add a `body` to the fixture JSON and assert it round-trips:

```go
		w.Write([]byte(`[
          {"number":30,"title":"feat(dashboard)","body":"the description","html_url":"u30","labels":[{"name":"area:tui"}],"updated_at":"2026-05-01T00:00:00Z","pull_request":null},
          {"number":31,"title":"is a PR","html_url":"u31","pull_request":{"url":"x"},"updated_at":"2026-05-02T00:00:00Z"}
        ]`))
```

and after the existing assertions:

```go
	// Body is what Eligible()'s empty-body gate reads; the list endpoint
	// returns it and we used to discard it.
	if issues[0].Body != "the description" {
		t.Errorf("Body = %q, want %q", issues[0].Body, "the description")
	}
```

Make the equivalent change in `internal/forge/forgejo_test.go`'s list-issues test, adding `"body":"the description"` to its fixture and the same assertion.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/forge -run 'ListIssues' -v`
Expected: FAIL — `Body = "" , want "the description"` in both.

- [ ] **Step 3: Write the minimal implementation**

In `internal/forge/github.go`, add the field to `ghIssue`:

```go
type ghIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	// ... rest unchanged
```

and map it in `ListOpenIssues`:

```go
		out = append(out, Issue{
			Forge:     "github",
			Repo:      owner + "/" + repo,
			Number:    i.Number,
			Title:     i.Title,
			Body:      i.Body,
			URL:       i.HTMLURL,
			Labels:    labels,
			Milestone: milestone,
			Updated:   i.UpdatedAt,
			Created:   i.CreatedAt,
		})
```

In `internal/forge/forgejo.go`, add `Body string \`json:"body"\`` to `fjIssue` and map it:

```go
		out = append(out, Issue{
			Forge: "forgejo", Repo: owner + "/" + repo,
			Number: i.Number, Title: i.Title, Body: i.Body, URL: i.HTMLURL,
			Labels: labels, Updated: i.UpdatedAt,
		})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/forge -run 'ListIssues' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/forge/github.go internal/forge/forgejo.go internal/forge/github_test.go internal/forge/forgejo_test.go
git commit -m "feat(forge): map issue Body in ListOpenIssues (#303)"
```

---

### Task 3: Clear `Body` at the outward-facing boundaries

**Files:**
- Modify: `internal/mcp/tools_read.go:239-249` (`handleListIssues`)
- Modify: `internal/api/repos.go:86-91` (`ReposHandler.detail`)
- Test: `internal/mcp/tools_read_test.go`, `internal/api/repos_test.go:60-84`

**Interfaces:**
- Consumes: `forge.Issue.Body` populated by Task 2.
- Produces: the invariant *"`Body` is populated for internal consumers and cleared at every outward-facing boundary"*, enforced by tests rather than convention.

**Why `handleListIssues` and not the MCP registration layer.** `internal/mcp/rest.go` exists so the MCP transport and the `/api/tools/` REST transport run **one** implementation of each tool. Stripping anywhere above the shared handler would leak bodies over REST the moment `list_issues` joins the REST toolset.

- [ ] **Step 1: Write the failing tests**

In `internal/mcp/tools_read_test.go`, add:

```go
func TestHandleListIssues_StripsBody(t *testing.T) {
	gh := newFakeFull("github")
	gh.issues = []forge.Issue{{Number: 1, Title: "t", Body: "a long issue body"}}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleListIssues(context.Background(), nil, listIssuesInput{
		Forge: "github", Owner: "o", Repo: "r",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Issues) != 1 {
		t.Fatalf("got %d issues", len(out.Issues))
	}
	// list_issues is a summary listing; bodies would multiply its token cost.
	if out.Issues[0].Body != "" {
		t.Errorf("Body = %q, want empty", out.Issues[0].Body)
	}
	if out.Issues[0].Title != "t" {
		t.Errorf("Title = %q, want the title preserved", out.Issues[0].Title)
	}
}
```

Match the fake's existing shape — if `fakeFull`/`fakeReader` has no settable issues field, add one (`issues []forge.Issue`) and return it from `ListOpenIssues` instead of the current fixed value, updating existing callers accordingly.

In `internal/api/repos_test.go`, extend `TestReposHandler_Detail_ReturnsRepoDetail`'s fake to return a body, and assert it is stripped:

```go
		Issues: func(_ context.Context, _, _, _ string) ([]forge.Issue, error) {
			return []forge.Issue{{Title: "open bug", Body: "a long issue body"}}, nil
		},
```

```go
	// FlowHub's catalogue polls this endpoint for titles and labels; issue
	// bodies would bloat every response.
	if got.Issues[0].Body != "" {
		t.Errorf("Body = %q, want empty", got.Issues[0].Body)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp ./internal/api -run 'StripsBody|ReturnsRepoDetail' -v`
Expected: FAIL — `Body = "a long issue body", want empty` in both.

- [ ] **Step 3: Write the minimal implementation**

In `internal/mcp/tools_read.go`, inside `handleListIssues` after the fetch:

```go
	issues, err := client.ListOpenIssues(ctx, in.Owner, in.Repo)
	if err != nil {
		return nil, listIssuesOutput{}, fmt.Errorf("list issues %s/%s: %w", in.Owner, in.Repo, err)
	}
	// Body is populated for internal consumers (the dispatcher's empty-body
	// gate). This is a summary listing, so it is cleared here — in the shared
	// handler, so the /api/tools/ REST transport gets the same treatment.
	for i := range issues {
		issues[i].Body = ""
	}
	return nil, listIssuesOutput{Issues: issues}, nil
```

In `internal/api/repos.go`, inside `detail`:

```go
	var issues []forge.Issue
	if h.Issues != nil {
		issues, _ = h.Issues(r.Context(), repo.Forge, repo.Owner, repo.Name)
	}
	// Same boundary rule as MCP list_issues: bodies are internal-only.
	for i := range issues {
		issues[i].Body = ""
	}
	writeJSON(w, RepoDetail{Repo: *repo, Sessions: repoSessions, Issues: issues})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp ./internal/api -run 'StripsBody|ReturnsRepoDetail' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_read.go internal/mcp/tools_read_test.go internal/api/repos.go internal/api/repos_test.go
git commit -m "feat(api): clear issue Body at the MCP and REST boundaries (#303)"
```

---

### Task 4: `CreateIssue` accepts labels, and ensures they exist

**Files:**
- Modify: `internal/forge/github.go:217-238` (`CreateIssue`), add `ensureLabels` helper
- Modify: `internal/forge/forgejo.go:208-229` (`CreateIssue`), add `ensureLabelIDs` helper
- Modify: `internal/capture/capture.go:76-88` (`IssueCreator` interface + `CaptureIssue`) — signature only, still passes `nil`
- Modify: `internal/mcp/tools.go:58` (`issueCreator` interface), `internal/mcp/tools_write.go:50` — signature only, still passes `nil`
- Modify: `internal/capture/capture_test.go:142-151`, `internal/mcp/tools_test.go:121-129` (fake signatures)
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (Issue, error)` on both `*forge.GithubClient` and `*forge.ForgejoClient`, and on the `capture.IssueCreator` and `mcp.issueCreator` consumer interfaces. Task 5 is what starts passing a non-nil value.

**This task is signature + forge behaviour only.** Call sites pass `nil` so the tree compiles and stays green; Task 5 threads the real label through. Splitting it this way keeps each task independently reviewable — a reviewer can reject the label policy without rejecting the plumbing.

**Label lookup is list-and-match, not probe-by-name.** The clients' `get` helper (`github.go:35-54`) returns a formatted error for any status ≥ 400 with no way to single out a 404, so a `GET /labels/{name}` probe cannot distinguish "absent" from "broken". Listing and matching by name needs no new error plumbing and is the same shape on both forges.

- [ ] **Step 1: Write the failing tests**

In `internal/forge/github_test.go`, replace `TestGithubCreateIssue` and add the two label cases:

```go
func TestGithubCreateIssue_PassesLabelsAndEnsuresTheyExist(t *testing.T) {
	var gotIssueBody map[string]any
	var createdLabel map[string]any
	labelListCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/labels":
			labelListCalls++
			w.Write([]byte(`[{"name":"bug"}]`)) // needs-enrichment absent
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/labels":
			_ = json.NewDecoder(r.Body).Decode(&createdLabel)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"needs-enrichment"}`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues":
			_ = json.NewDecoder(r.Body).Decode(&gotIssueBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":142,"title":"flicker","html_url":"https://github.com/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freaxnx01", "bridge", "flicker", "", []string{"needs-enrichment"})
	if err != nil {
		t.Fatal(err)
	}
	if labelListCalls != 1 {
		t.Errorf("label list calls = %d, want 1", labelListCalls)
	}
	if createdLabel["name"] != "needs-enrichment" {
		t.Errorf("missing label was not created: %+v", createdLabel)
	}
	// The label must ride along on the create POST — a follow-up AddLabels call
	// would leave a window in which the issue is live and unlabeled.
	labels, _ := gotIssueBody["labels"].([]any)
	if len(labels) != 1 || labels[0] != "needs-enrichment" {
		t.Errorf("labels sent on create: %+v", gotIssueBody["labels"])
	}
	if is.Number != 142 || is.URL != "https://github.com/freaxnx01/bridge/issues/142" {
		t.Errorf("issue: %+v", is)
	}
}

func TestGithubCreateIssue_ExistingLabelIsNotRecreated(t *testing.T) {
	labelCreates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/labels":
			w.Write([]byte(`[{"name":"needs-enrichment"},{"name":"bug"}]`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/labels":
			labelCreates++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"needs-enrichment"}`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":1,"title":"t","html_url":"u","updated_at":"2026-07-22T10:00:00Z"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	if _, err := c.CreateIssue(context.Background(), "freaxnx01", "bridge", "t", "", []string{"needs-enrichment"}); err != nil {
		t.Fatal(err)
	}
	if labelCreates != 0 {
		t.Errorf("label create calls = %d, want 0 when the label already exists", labelCreates)
	}
}

func TestGithubCreateIssue_NoLabelsSkipsTheLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/labels") {
			t.Fatalf("no labels requested, but the client hit %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":9,"title":"t","html_url":"u","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	if _, err := c.CreateIssue(context.Background(), "freaxnx01", "bridge", "t", "", nil); err != nil {
		t.Fatal(err)
	}
}
```

In `internal/forge/forgejo_test.go`, the equivalent — note Forgejo sends label **IDs**:

```go
func TestForgejoCreateIssue_SendsLabelIDsAndCreatesMissingLabel(t *testing.T) {
	var gotIssueBody map[string]any
	var createdLabel map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/repos/freax/notes/labels":
			w.Write([]byte(`[{"id":3,"name":"bug"}]`)) // needs-enrichment absent
		case r.Method == "POST" && r.URL.Path == "/api/v1/repos/freax/notes/labels":
			_ = json.NewDecoder(r.Body).Decode(&createdLabel)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":11,"name":"needs-enrichment"}`))
		case r.Method == "POST" && r.URL.Path == "/api/v1/repos/freax/notes/issues":
			_ = json.NewDecoder(r.Body).Decode(&gotIssueBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"title":"rough idea","html_url":"https://fj.example/freax/notes/issues/7","updated_at":"2026-07-22T10:00:00Z"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freax", "notes", "rough idea", "", []string{"needs-enrichment"})
	if err != nil {
		t.Fatal(err)
	}
	if createdLabel["name"] != "needs-enrichment" {
		t.Errorf("missing label was not created: %+v", createdLabel)
	}
	// Forgejo's CreateIssueOption.labels is "list of label ids" (int64), not
	// names — verified against the instance's swagger.v1.json.
	labels, _ := gotIssueBody["labels"].([]any)
	if len(labels) != 1 || labels[0] != float64(11) {
		t.Errorf("labels sent on create = %+v, want [11]", gotIssueBody["labels"])
	}
	if is.Number != 7 {
		t.Errorf("issue: %+v", is)
	}
}
```

Keep a Forgejo equivalent of `NoLabelsSkipsTheLookup` too, asserting no `/labels` request is made when `labels` is nil.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/forge -run CreateIssue -v`
Expected: compile error — `too many arguments in call to c.CreateIssue`. That is the failure; it becomes assertion failures once the signature lands.

- [ ] **Step 3: Implement the GitHub client**

In `internal/forge/github.go`, add the helper and rewrite `CreateIssue`:

```go
// ensureLabels makes sure every name exists as a label on owner/repo, creating
// the ones that don't. Capture targets are not guaranteed to define the labels
// bridge stamps at intake — game-huusli-jagd did not define needs-enrichment —
// and an issue created with an undefined label is rejected by the forge.
//
// It lists and matches rather than probing GET /labels/{name}: the get helper
// collapses every status >= 400 into one error, so a probe cannot tell an
// absent label from a broken request.
func (c *GithubClient) ensureLabels(ctx context.Context, owner, repo string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var existing []struct {
		Name string `json:"name"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels?per_page=100"
	if err := c.get(ctx, path, &existing); err != nil {
		return fmt.Errorf("list labels %s/%s: %w", owner, repo, err)
	}
	have := make(map[string]bool, len(existing))
	for _, l := range existing {
		have[l.Name] = true
	}
	for _, name := range names {
		if have[name] {
			continue
		}
		var created struct {
			Name string `json:"name"`
		}
		createPath := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels"
		if err := c.post(ctx, createPath, map[string]any{"name": name, "color": "ededed"}, &created); err != nil {
			return fmt.Errorf("create label %q on %s/%s: %w", name, owner, repo, err)
		}
	}
	return nil
}

// CreateIssue creates an issue on owner/repo and returns the minimal Issue.
// labels ride along on the create request rather than a follow-up AddLabels
// call: a second call leaves a window in which the issue is live and
// unlabeled, which is exactly what the dispatcher must never see.
func (c *GithubClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (Issue, error) {
	if err := c.ensureLabels(ctx, owner, repo, labels); err != nil {
		return Issue{}, err
	}
	req := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		req["labels"] = labels
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := c.post(ctx, "/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:   "github",
		Repo:    owner + "/" + repo,
		Number:  raw.Number,
		Title:   raw.Title,
		URL:     raw.HTMLURL,
		Updated: raw.UpdatedAt,
	}, nil
}
```

`url` and `fmt` are already imported in this file.

> Note: the orphaned `// CreateIssue creates an issue on owner/repo and returns the minimal Issue.` comment currently sitting above `put` at `github.go:144` is pre-existing. The spec records it as noted-not-fixed; leave it alone unless you are told otherwise.

- [ ] **Step 4: Implement the Forgejo client**

In `internal/forge/forgejo.go`:

```go
// ensureLabelIDs resolves each name to its label ID on owner/repo, creating any
// label that does not exist. Forgejo/Gitea's CreateIssueOption.labels is a list
// of label *ids*, so this lookup is mandatory here, not merely defensive.
func (c *ForgejoClient) ensureLabelIDs(ctx context.Context, owner, repo string, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var existing []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	path := "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels"
	if err := c.get(ctx, path, &existing); err != nil {
		return nil, fmt.Errorf("list labels %s/%s: %w", owner, repo, err)
	}
	byName := make(map[string]int64, len(existing))
	for _, l := range existing {
		byName[l.Name] = l.ID
	}
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		if id, ok := byName[name]; ok {
			ids = append(ids, id)
			continue
		}
		var created struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := c.post(ctx, path, map[string]any{"name": name, "color": "#ededed"}, &created); err != nil {
			return nil, fmt.Errorf("create label %q on %s/%s: %w", name, owner, repo, err)
		}
		ids = append(ids, created.ID)
	}
	return ids, nil
}

// CreateIssue creates an issue on owner/repo via Forgejo/Gitea and returns the
// minimal Issue. labels are names; they are resolved to ids (and created when
// absent) before the create request, which carries them atomically.
func (c *ForgejoClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (Issue, error) {
	ids, err := c.ensureLabelIDs(ctx, owner, repo, labels)
	if err != nil {
		return Issue{}, err
	}
	req := map[string]any{"title": title, "body": body}
	if len(ids) > 0 {
		req["labels"] = ids
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := c.post(ctx, "/api/v1/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:   "forgejo",
		Repo:    owner + "/" + repo,
		Number:  raw.Number,
		Title:   raw.Title,
		URL:     raw.HTMLURL,
		Updated: raw.UpdatedAt,
	}, nil
}
```

Verify `url` and `fmt` are imported in `forgejo.go`; add them if not.

- [ ] **Step 5: Update the consumer interfaces and call sites to keep the tree compiling**

Signature only — still `nil`. In `internal/capture/capture.go`:

```go
// IssueCreator is the consumer interface for CaptureIssue. Both
// *forge.GithubClient and *forge.ForgejoClient satisfy it.
type IssueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (forge.Issue, error)
}

// CaptureIssue creates an issue on the chosen repo's forge and returns the
// created Issue. body is passed through to the forge as-is.
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title, body string) (forge.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return forge.Issue{}, fmt.Errorf("empty issue title")
	}
	return w.CreateIssue(ctx, owner, repo, title, body, nil)
}
```

In `internal/mcp/tools.go:58`, widen `issueCreator` the same way. In `internal/mcp/tools_write.go:50`:

```go
	issue, err := issues.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body, nil)
```

Update both fakes to match the new signature: `internal/capture/capture_test.go:148` (`fakeIssueCreator.CreateIssue`) and `internal/mcp/tools_test.go:121` (`fakeIssues.CreateIssue`) each gain a trailing `labels []string` parameter. Record what the fakes receive — Task 5 asserts on it:

```go
// internal/capture/capture_test.go
type fakeIssueCreator struct {
	ret        forge.Issue
	gotOwner   string
	gotRepo    string
	gotTitle   string
	gotBody    string
	gotLabels  []string
}

func (f *fakeIssueCreator) CreateIssue(_ context.Context, owner, repo, title, body string, labels []string) (forge.Issue, error) {
	f.gotOwner, f.gotRepo, f.gotTitle, f.gotBody, f.gotLabels = owner, repo, title, body, labels
	return f.ret, nil
}
```

Keep whatever fields the existing fake already has; add `gotLabels` rather than replacing its shape wholesale. Do the same for `fakeIssues` in `internal/mcp/tools_test.go`, adding a `gotLabels []string` field set on each call.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/forge -run CreateIssue -v`
Expected: PASS, all cases.

- [ ] **Step 7: Run the full suite**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all green. `go build ./...` must also succeed — this task touched a shared signature.

- [ ] **Step 8: Commit**

```bash
git add internal/forge internal/capture internal/mcp
git commit -m "feat(forge): accept labels on CreateIssue and ensure they exist (#303)"
```

---

### Task 5: Every intake path stamps `needs-enrichment`

**Files:**
- Modify: `internal/capture/capture.go` (`CaptureIssue` passes the label)
- Modify: `internal/mcp/tools_write.go:15-60` (`handleCreateIssue` passes the label; the draft reports it)
- Test: `internal/capture/capture_test.go`, `internal/api/capture_test.go`, `internal/mcp/tools_write_test.go`

**Interfaces:**
- Consumes: `CreateIssue(..., labels []string)` from Task 4; `dispatch.LabelNeedsEnrichment` (`internal/dispatch/eligible.go:13`).
- Produces: the guarantee that every bridge-originated issue is born carrying `needs-enrichment`. Nothing later depends on it.

- [ ] **Step 1: Write the failing tests**

In `internal/capture/capture_test.go`:

```go
func TestCaptureIssue_StampsNeedsEnrichment(t *testing.T) {
	fake := &fakeIssueCreator{ret: forge.Issue{Number: 7, URL: "https://forge/issues/7"}}
	if _, err := CaptureIssue(context.Background(), fake, "freaxnx01", "bridge", "Login 500", "detail"); err != nil {
		t.Fatal(err)
	}
	// Without this, a captured issue is dispatch-eligible the moment it is
	// filed — the bug in #303.
	if len(fake.gotLabels) != 1 || fake.gotLabels[0] != dispatch.LabelNeedsEnrichment {
		t.Errorf("labels = %+v, want [%s]", fake.gotLabels, dispatch.LabelNeedsEnrichment)
	}
}
```

In `internal/api/capture_test.go` — AC #4, pinning the label on the HTTP capture path end to end through the handler:

```go
func TestCaptureIssue_HandlerPathCreatesWithNeedsEnrichment(t *testing.T) {
	fake := &fakeIssueCreator{ret: forge.Issue{Number: 1, URL: "https://forge/issues/1"}}
	h := &CaptureHandler{
		Issue: func(ctx context.Context, p IssueParams) (forge.Issue, error) {
			return capture.CaptureIssue(ctx, fake, "freaxnx01", "bridge", p.Title, p.Body)
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue",
		strings.NewReader(`{"alias":"br","title":"Login 500","body":"the detail"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(fake.gotLabels) != 1 || fake.gotLabels[0] != dispatch.LabelNeedsEnrichment {
		t.Errorf("labels = %+v, want [%s]", fake.gotLabels, dispatch.LabelNeedsEnrichment)
	}
}
```

This test needs a `fakeIssueCreator` in package `api`. Declare a local one rather than exporting the capture package's test fake (test helpers don't cross packages):

```go
type fakeIssueCreator struct {
	ret       forge.Issue
	gotLabels []string
}

func (f *fakeIssueCreator) CreateIssue(_ context.Context, _, _, _, _ string, labels []string) (forge.Issue, error) {
	f.gotLabels = labels
	return f.ret, nil
}
```

In `internal/mcp/tools_write_test.go`:

```go
func TestHandleCreateIssue_StampsNeedsEnrichment(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// create_issue is a second intake path; leaving it unlabeled reproduces
	// #303 through a different door.
	if len(gh.gotLabels) != 1 || gh.gotLabels[0] != dispatch.LabelNeedsEnrichment {
		t.Errorf("labels = %+v, want [%s]", gh.gotLabels, dispatch.LabelNeedsEnrichment)
	}
}

func TestHandleCreateIssue_DraftReportsTheLabel(t *testing.T) {
	gh := newFakeFull("github")
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	// A draft that hides the label misrepresents the issue the caller is about
	// to confirm.
	if len(out.Labels) != 1 || out.Labels[0] != dispatch.LabelNeedsEnrichment {
		t.Errorf("draft labels = %+v, want [%s]", out.Labels, dispatch.LabelNeedsEnrichment)
	}
}
```

Reach the fake's recorded labels through whatever accessor `newFakeFull` exposes — if `fakeFull` embeds `fakeIssues`, `gh.gotLabels` resolves through the embedded struct; otherwise add the field where the `CreateIssue` method lives.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/capture ./internal/api ./internal/mcp -run 'NeedsEnrichment|DraftReportsTheLabel' -v`
Expected: FAIL — `labels = [], want [needs-enrichment]`, and a compile error for the not-yet-existing `out.Labels` field.

- [ ] **Step 3: Implement the capture path**

In `internal/capture/capture.go`, import `"github.com/freaxnx01/bridge/internal/dispatch"` and pass the label:

```go
// CaptureIssue creates an issue on the chosen repo's forge and returns the
// created Issue. body is passed through to the forge as-is.
//
// Every captured issue is born carrying needs-enrichment: the dispatcher's
// "running /enrich is the approval" contract holds only if intake stamps it,
// and a capture arrives with no labels at all.
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title, body string) (forge.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return forge.Issue{}, fmt.Errorf("empty issue title")
	}
	return w.CreateIssue(ctx, owner, repo, title, body, []string{dispatch.LabelNeedsEnrichment})
}
```

- [ ] **Step 4: Implement the MCP path**

In `internal/mcp/tools_write.go`, add `Labels` to the output struct, populate it in both the draft and the created branch, and pass it to the client:

```go
type createIssueOutput struct {
	Draft  bool         `json:"draft"`
	Forge  string       `json:"forge"`
	Owner  string       `json:"owner"`
	Repo   string       `json:"repo"`
	Title  string       `json:"title"`
	Body   string       `json:"body,omitempty"`
	Labels []string     `json:"labels,omitempty"`
	Issue  *forge.Issue `json:"issue,omitempty"`
}

func (d Deps) handleCreateIssue(ctx context.Context, _ *mcp.CallToolRequest, in createIssueInput) (*mcp.CallToolResult, createIssueOutput, error) {
	// Intake stamps needs-enrichment on every path, create_issue included.
	// There is no opt-out: removing the label is a deliberate act, which is
	// the fail-closed direction.
	labels := []string{dispatch.LabelNeedsEnrichment}
	draft := createIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
		Labels: labels,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issues, ok := client.(issueCreator)
	if !ok {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q does not support creating issues", in.Forge)
	}
	issue, err := issues.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body, labels)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "create_issue", Confirm: true, Outcome: "error"})
		return nil, createIssueOutput{}, fmt.Errorf("create issue %s/%s: %w", in.Owner, in.Repo, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "create_issue", Confirm: true, Outcome: "success"})
	return nil, createIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
		Labels: labels,
		Issue:  &issue,
	}, nil
}
```

Add the `internal/dispatch` import to this file.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/capture ./internal/api ./internal/mcp -v`
Expected: PASS, including the pre-existing `TestHandleCreateIssue_DraftDoesNotCreate` and `TestCaptureIssue_ForwardsBody`.

- [ ] **Step 6: Run the full suite**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/capture internal/api internal/mcp
git commit -m "fix(capture): stamp needs-enrichment on every intake path (#303)"
```

---

### Task 6: Document the rules and verify against the live backlog

**Files:**
- Modify: `docs/dispatch.md` (the **Eligibility** list and the **Labels** table)
- Modify: `CHANGELOG.md` (`[Unreleased]`)

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces: nothing code-level. This task closes AC #3, which cannot be asserted in a unit test.

- [ ] **Step 1: Update the eligibility documentation**

In `docs/dispatch.md`, insert a new first rule in the **Eligibility** list and renumber the rest (the existing six become 2–7):

```markdown
1. **Non-empty body** — The issue body must not be blank or whitespace-only. Checked before the label rules: an empty issue is unfit for dispatch for a reason that has nothing to do with its labels, and this is the backstop for any intake path that fails to stamp `needs-enrichment`. Reason string: `empty body`.
```

In the **Labels** table, change the `needs-enrichment` row's *Set by* column:

```markdown
| `needs-enrichment` | Issue lacks clear task description; skip until enriched | Bridge intake (`/api/capture/issue`, MCP `create_issue`) or manual |
```

- [ ] **Step 2: Add the changelog entries**

Under `[Unreleased]` in `CHANGELOG.md`, following the existing Keep a Changelog sections:

```markdown
### Fixed

- Capture-created issues are no longer dispatch-eligible without enrichment: every intake path (`POST /api/capture/issue`, MCP `create_issue`) now stamps `needs-enrichment`, and `bridge dispatch` rejects an issue with a blank body regardless of its labels (#303)

### Added

- `CreateIssue` accepts labels and creates any that the target repo does not define
- Issue bodies are populated by `ListOpenIssues` for internal consumers and cleared at the MCP `list_issues` and `GET /api/repos/{repo}` boundaries
```

- [ ] **Step 3: Verify against the live backlog — AC #3**

Run: `go run ./cmd/bridge dispatch --dry-run`

Expected: `game-tschau-sepp #31` appears as `SKIP (empty body)`. Capture the relevant output lines and paste them into the PR description — this is the only evidence for AC #3, and it cannot be asserted in a unit test.

If `#31` instead shows as `SKIP (needs-enrichment)`, the empty-body rule is not first in `Eligible()`. If it shows as `dispatch`, `ListOpenIssues` is not mapping `Body` — re-check Task 2.

- [ ] **Step 4: Verify intake end-to-end (optional but preferred)**

If a scratch repo is available, create an issue through the real path and confirm it carries the label:

```bash
go run ./cmd/bridge serve &          # or hit an already-running instance
curl -sS -X POST localhost:<port>/api/capture/issue \
  -H "Authorization: Bearer $BRIDGE_API_TOKEN" \
  -d '{"alias":"<scratch-repo-alias>","title":"intake label smoke test","body":"x"}'
gh issue view <n> -R <owner>/<scratch> --json labels
```

Expected: `needs-enrichment` present. Close the scratch issue afterwards. Skip this step if no scratch repo is available — say so in the PR rather than testing against a real project repo.

- [ ] **Step 5: Run the full suite one final time**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./... && go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add docs/dispatch.md CHANGELOG.md
git commit -m "docs(dispatch): document the empty-body gate and intake labeling (#303)"
```

---

## Verification against the acceptance criteria

| AC | Covered by |
|---|---|
| Issue via `/api/capture/issue` carries `needs-enrichment` | Task 5, Steps 1 & 3 |
| Issue via MCP `create_issue` carries it; draft reports it | Task 5, Steps 1 & 4 |
| A missing label is created rather than failing the capture | Task 4, Steps 3 & 4 |
| `Eligible()` rejects blank/whitespace body with `empty body` | Task 1 |
| `ListOpenIssues` populates `Body` on both forges | Task 2 |
| `Body` empty in `list_issues` and `GET /api/repos/{repo}` | Task 3 |
| `dispatch --dry-run` shows `#31` as `SKIP (empty body)` | Task 6, Step 3 |
| Formatting, vet, lint and `-race` all clean | Every task's final step |
