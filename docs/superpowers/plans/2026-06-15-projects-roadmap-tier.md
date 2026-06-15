# Projects v2 Roadmap Tier (Plan 1b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Status-grouped Roadmap tier to the bridge-nav overview, fed by a new GitHub Projects v2 GraphQL query, showing the configured board's items by Status (Todo / In Progress / Done).

**Architecture:** Add a minimal GraphQL capability + `ListProjectV2Items` to the GitHub forge client. Reshape the `internal/overview` `FetchRoadmap` seam from `[]RankedItem` to `[]RoadmapItem` (Status-bearing, unscored); `Build` stores them in `Snapshot.Roadmap` sorted by Status. nav renders a third tier between "what matters now" and "Inbox". `cmd/bridge` wires the provider from `BRIDGE_PROJECT=owner/number` using a `project`-scoped token.

**Tech Stack:** Go (stdlib `net/http`/`encoding/json`/`testing`/`httptest`), GitHub GraphQL API, Bubble Tea/lipgloss. Spec: `docs/superpowers/specs/2026-06-15-projects-roadmap-tier-design.md`.

**Spec correction (discovered during planning):** the spec said "reuse the per-owner GitHub token". In reality `cmd/bridge`'s `clientFor` reads the **ambient** `GH_TOKEN`, which (when nav is launched from a public repo dir) lacks `project` scope. So the provider uses an explicit **`BRIDGE_PROJECT_TOKEN`** env (fallback `GH_TOKEN`); that token must have `project` scope. Everything else matches the spec.

---

## File Structure

- **Modify** `internal/forge/github.go` — add `graphqlPost`, `ProjectItem`, `ListProjectV2Items`.
- **Modify** `internal/forge/github_test.go` — GraphQL stub tests.
- **Modify** `internal/overview/overview.go` — `RoadmapItem`, `Snapshot.Roadmap`, reshape `Build`'s roadmap branch + Status sort.
- **Modify** `internal/overview/captures.go` — reshape `Config.FetchRoadmap` type.
- **Modify** `internal/overview/build_test.go` — roadmap-grouping test.
- **Modify** `internal/nav/overview.go` — `viewRoadmap` helper + call from `viewOverview`.
- **Modify** `internal/nav/flow_test.go` — 3-tier golden test + empty-omit test; `internal/nav/testdata/overview_with_roadmap.golden` (new).
- **Modify** `cmd/bridge/nav.go` — `BRIDGE_PROJECT` parse + `FetchRoadmap` closure.

---

## Task 1: forge GraphQL + `ListProjectV2Items`

**Files:**
- Modify: `internal/forge/github.go`
- Test: `internal/forge/github_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/forge/github_test.go`:

```go
func TestGithubListProjectV2Items_PaginatesAndMaps(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		page++
		if page == 1 {
			w.Write([]byte(`{"data":{"user":{"projectV2":{"items":{
              "pageInfo":{"hasNextPage":true,"endCursor":"C1"},
              "nodes":[
                {"content":{"__typename":"Issue","title":"an issue","url":"https://x/1","repository":{"nameWithOwner":"freaxnx01/bridge"}},
                 "fieldValues":{"nodes":[{"__typename":"ProjectV2ItemFieldSingleSelectValue","name":"In Progress","field":{"name":"Status"}}]}},
                {"content":{"__typename":"DraftIssue","title":"a draft idea"},
                 "fieldValues":{"nodes":[{"__typename":"ProjectV2ItemFieldSingleSelectValue","name":"Todo","field":{"name":"Status"}}]}}
              ]}}}}}`))
			return
		}
		w.Write([]byte(`{"data":{"user":{"projectV2":{"items":{
          "pageInfo":{"hasNextPage":false,"endCursor":"C2"},
          "nodes":[
            {"content":{"__typename":"PullRequest","title":"a pr","url":"https://x/2","repository":{"nameWithOwner":"freaxnx01/agent-os"}},
             "fieldValues":{"nodes":[]}}
          ]}}}}}`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	items, err := c.ListProjectV2Items(context.Background(), "freaxnx01", 5)
	if err != nil {
		t.Fatal(err)
	}
	if page != 2 {
		t.Errorf("expected 2 pages fetched, got %d", page)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].Type != "Issue" || items[0].Repo != "freaxnx01/bridge" || items[0].URL != "https://x/1" || items[0].Status != "In Progress" {
		t.Errorf("item[0]: %+v", items[0])
	}
	if items[1].Type != "DraftIssue" || items[1].Title != "a draft idea" || items[1].Status != "Todo" || items[1].Repo != "" {
		t.Errorf("item[1]: %+v", items[1])
	}
	if items[2].Type != "PullRequest" || items[2].Repo != "freaxnx01/agent-os" || items[2].Status != "" {
		t.Errorf("item[2]: %+v", items[2])
	}
}

func TestGithubGraphQL_SurfacesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"Your token has not been granted the required scopes"}]}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)
	_, err := c.ListProjectV2Items(context.Background(), "freaxnx01", 5)
	if err == nil {
		t.Fatal("expected error from graphql errors array")
	}
	if !strings.Contains(err.Error(), "scopes") {
		t.Errorf("error should surface the graphql message, got: %v", err)
	}
}
```

Add `"strings"` to the test file's imports if not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/forge/ -run 'TestGithubListProjectV2Items|TestGithubGraphQL' -v`
Expected: FAIL — `c.ListProjectV2Items` undefined.

- [ ] **Step 3: Implement the GraphQL helper + query**

Add to `internal/forge/github.go` (it already imports `bytes`? — if not, add `"bytes"`; `encoding/json`, `fmt`, `net/http` are already used by the file):

```go
// ProjectItem is one GitHub Projects v2 board item, flattened for the roadmap.
type ProjectItem struct {
	Type   string // "Issue" | "DraftIssue" | "PullRequest"
	Repo   string // owner/name; "" for DraftIssue
	Title  string
	URL    string // "" for DraftIssue
	Status string // the board's Status single-select value; "" if unset
}

// graphqlPost issues a GraphQL query against <baseURL>/graphql and unmarshals
// the "data" object into out. A non-empty "errors" array is returned as an
// error (so INSUFFICIENT_SCOPES and similar surface clearly).
func (c *GithubClient) graphqlPost(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github graphql: %s: %s", resp.Status, string(body))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("github graphql: %s", env.Errors[0].Message)
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

const projectV2ItemsQuery = `query($owner:String!, $number:Int!, $cursor:String){
  user(login:$owner){
    projectV2(number:$number){
      items(first:100, after:$cursor){
        pageInfo{ hasNextPage endCursor }
        nodes{
          content{
            __typename
            ... on Issue{ title url repository{ nameWithOwner } }
            ... on PullRequest{ title url repository{ nameWithOwner } }
            ... on DraftIssue{ title }
          }
          fieldValues(first:20){
            nodes{
              __typename
              ... on ProjectV2ItemFieldSingleSelectValue{ name field{ ... on ProjectV2FieldCommon{ name } } }
            }
          }
        }
      }
    }
  }
}`

// ListProjectV2Items returns every item on the user-level Projects v2 board
// (owner, number), flattened to ProjectItem with its Status single-select
// value. It paginates 100 at a time.
func (c *GithubClient) ListProjectV2Items(ctx context.Context, owner string, number int) ([]ProjectItem, error) {
	var out []ProjectItem
	cursor := ""
	for {
		vars := map[string]any{"owner": owner, "number": number, "cursor": nil}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var data struct {
			User struct {
				ProjectV2 struct {
					Items struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							Content struct {
								Typename   string `json:"__typename"`
								Title      string `json:"title"`
								URL        string `json:"url"`
								Repository struct {
									NameWithOwner string `json:"nameWithOwner"`
								} `json:"repository"`
							} `json:"content"`
							FieldValues struct {
								Nodes []struct {
									Typename string `json:"__typename"`
									Name     string `json:"name"`
									Field    struct {
										Name string `json:"name"`
									} `json:"field"`
								} `json:"nodes"`
							} `json:"fieldValues"`
						} `json:"nodes"`
					} `json:"items"`
				} `json:"projectV2"`
			} `json:"user"`
		}
		if err := c.graphqlPost(ctx, projectV2ItemsQuery, vars, &data); err != nil {
			return nil, fmt.Errorf("list project v2 items %s/%d: %w", owner, number, err)
		}
		for _, n := range data.User.ProjectV2.Items.Nodes {
			item := ProjectItem{
				Type:  n.Content.Typename,
				Title: n.Content.Title,
				URL:   n.Content.URL,
				Repo:  n.Content.Repository.NameWithOwner,
			}
			for _, fv := range n.FieldValues.Nodes {
				if fv.Typename == "ProjectV2ItemFieldSingleSelectValue" && fv.Field.Name == "Status" {
					item.Status = fv.Name
					break
				}
			}
			out = append(out, item)
		}
		if !data.User.ProjectV2.Items.PageInfo.HasNextPage {
			break
		}
		cursor = data.User.ProjectV2.Items.PageInfo.EndCursor
	}
	return out, nil
}
```

Verify `io` is imported in `github.go` (the existing `get` uses `io.ReadAll`, so it is). Add `"bytes"` if missing.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/forge/ -run 'TestGithubListProjectV2Items|TestGithubGraphQL' -v`
Expected: PASS (both).

- [ ] **Step 5: Format/vet + commit**

Run: `gofmt -l internal/forge/ && go vet ./internal/forge/`
Expected: clean.

```bash
git add internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): GitHub GraphQL + ListProjectV2Items

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: overview `RoadmapItem` + `Snapshot.Roadmap` + Build

**Files:**
- Modify: `internal/overview/overview.go`
- Modify: `internal/overview/captures.go` (Config.FetchRoadmap type)
- Test: `internal/overview/build_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/overview/build_test.go`:

```go
func TestBuild_Roadmap_StatusOrderedNoScoring(t *testing.T) {
	cfg := Config{
		Now: func() time.Time { return time.Now() },
		FetchRoadmap: func(_ context.Context) ([]RoadmapItem, error) {
			return []RoadmapItem{
				{Repo: "bridge", Title: "done thing", Status: "Done"},
				{Repo: "bridge", Title: "todo thing", Status: "Todo"},
				{Repo: "bridge", Title: "wip thing", Status: "In Progress"},
				{Repo: "bridge", Title: "weird", Status: "Backlog"}, // unknown -> last
			}, nil
		},
	}
	snap, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Roadmap) != 4 {
		t.Fatalf("roadmap = %d, want 4", len(snap.Roadmap))
	}
	gotOrder := []string{snap.Roadmap[0].Status, snap.Roadmap[1].Status, snap.Roadmap[2].Status, snap.Roadmap[3].Status}
	want := []string{"Todo", "In Progress", "Done", "Backlog"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("status order[%d] = %q, want %q", i, gotOrder[i], want[i])
		}
	}
	// roadmap items must NOT leak into the weighted/ranked tiers
	if len(snap.Ranked) != 0 || len(snap.NeedsWeighting) != 0 {
		t.Errorf("roadmap leaked into ranked/needs-weighting: %+v %+v", snap.Ranked, snap.NeedsWeighting)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/overview/ -run TestBuild_Roadmap -v`
Expected: FAIL — `RoadmapItem` undefined / `Snapshot.Roadmap` undefined / `FetchRoadmap` wrong type.

- [ ] **Step 3: Reshape the types, Config, and Build**

In `internal/overview/overview.go`, add the type and the Snapshot field:

```go
// RoadmapItem is one GitHub Projects v2 board item, grouped by Status (not
// weighted — the roadmap tier is distinct from the ranked "what matters now").
type RoadmapItem struct {
	Repo   string
	Title  string
	URL    string
	Status string
}
```

Add `Roadmap []RoadmapItem` to the `Snapshot` struct (after `Inbox`).

Add the Status ordering near the scoring constants (or in overview.go):

```go
// statusOrder is the canonical board column order; unknown statuses sort after,
// preserving board order among themselves (stable sort).
var statusOrder = []string{"Todo", "In Progress", "Done"}

func statusRank(s string) int {
	for i, v := range statusOrder {
		if v == s {
			return i
		}
	}
	return len(statusOrder)
}
```

In `internal/overview/captures.go`, change the `Config.FetchRoadmap` field type:

```go
	FetchRoadmap func(ctx context.Context) ([]RoadmapItem, error) // nil => no roadmap tier
```

In `internal/overview/overview.go`, **replace** the existing `if cfg.FetchRoadmap != nil { ... }` block in `Build` (the Plan 1a card-scoring branch) with:

```go
	if cfg.FetchRoadmap != nil {
		items, err := cfg.FetchRoadmap(ctx)
		if err != nil {
			return snap, fmt.Errorf("fetch roadmap: %w", err)
		}
		sort.SliceStable(items, func(i, j int) bool {
			return statusRank(items[i].Status) < statusRank(items[j].Status)
		})
		snap.Roadmap = items
	}
```

(`sort` and `fmt` are already imported by `Build`.) Confirm no leftover references to the removed card-scoring code (e.g. a now-unused variable) — `go vet` / build will flag them.

- [ ] **Step 4: Run the test + full package**

Run: `go test ./internal/overview/ -v`
Expected: PASS (the new roadmap test plus all Task-1a tests — scoring/captures/build).

- [ ] **Step 5: Commit**

```bash
git add internal/overview/overview.go internal/overview/captures.go internal/overview/build_test.go
git commit -m "feat(overview): Status-grouped Roadmap tier in Snapshot/Build

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: nav — render the Roadmap tier

**Files:**
- Modify: `internal/nav/overview.go` (add `viewRoadmap`, call from `viewOverview`)
- Modify: `internal/nav/flow_test.go` (3-tier golden + empty-omit test)
- Create: `internal/nav/testdata/overview_with_roadmap.golden` (via `-update`)

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/flow_test.go`:

```go
func overviewWithRoadmap() overview.Snapshot {
	s := fixedOverview()
	s.Roadmap = []overview.RoadmapItem{
		{Repo: "bridge", Title: "todo one", Status: "Todo"},
		{Repo: "bridge", Title: "todo two", Status: "Todo"},
		{Repo: "agent-pipeline", Title: "wip one", Status: "In Progress"},
		{Repo: "bridge", Title: "done one", Status: "Done"},
	}
	return s
}

func TestFlow_OverviewWithRoadmap_Golden(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return overviewWithRoadmap(), nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	s.resolve()
	assertGolden(t, "overview_with_roadmap", s.frame())
}

func TestViewOverview_EmptyRoadmapOmitsTier(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.width, m.height = 120, 40
	m.overviewState = loadOK
	m.overview = fixedOverview() // no Roadmap
	if strings.Contains(m.viewOverview(), "Roadmap") {
		t.Errorf("empty roadmap should omit the tier:\n%s", m.viewOverview())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestFlow_OverviewWithRoadmap_Golden|TestViewOverview_EmptyRoadmapOmitsTier' -v`
Expected: FAIL — golden missing for the first; the second fails only if a stray "Roadmap" already renders (it shouldn't yet) — it may PASS pre-implementation, which is fine (it guards the omit behavior you're about to preserve).

- [ ] **Step 3: Implement `viewRoadmap` and call it**

In `internal/nav/overview.go`, add the helper:

```go
const roadmapGroupCap = 6 // max items listed per Status group before "↓ N more"

// viewRoadmap renders the board's items grouped by Status (board order). Done
// collapses to a count; other groups list up to roadmapGroupCap items.
func (m Model) viewRoadmap(w int) string {
	var b strings.Builder
	b.WriteString(stAccent.Render("Roadmap") + "\n")
	for _, status := range overview.RoadmapStatuses(m.overview.Roadmap) {
		group := overview.RoadmapByStatus(m.overview.Roadmap, status)
		if status == "Done" {
			b.WriteString(stMuted.Render(fmt.Sprintf("Done · %d", len(group))) + "\n")
			continue
		}
		b.WriteString(stText.Render(fmt.Sprintf("%s (%d)", status, len(group))) + "\n")
		for i, it := range group {
			if i >= roadmapGroupCap {
				b.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(group)-roadmapGroupCap)) + "\n")
				break
			}
			b.WriteString("  " + stText.Render(fmt.Sprintf("• %-14s %s", trunc(it.Repo, 14), it.Title)) + "\n")
		}
	}
	return panel(w, "Roadmap", strings.TrimRight(b.String(), "\n"))
}
```

Add the two small helpers to `internal/overview/overview.go` (so the Status grouping/order lives with the data, and nav stays render-only):

```go
// RoadmapStatuses returns the distinct statuses present in items, in board
// order (statusOrder first, then any others in first-seen order).
func RoadmapStatuses(items []RoadmapItem) []string {
	seen := map[string]bool{}
	var known, other []string
	for _, s := range statusOrder {
		for _, it := range items {
			if it.Status == s && !seen[s] {
				seen[s] = true
				known = append(known, s)
			}
		}
	}
	for _, it := range items {
		if !seen[it.Status] {
			seen[it.Status] = true
			other = append(other, it.Status)
		}
	}
	return append(known, other...)
}

// RoadmapByStatus returns the items with the given Status, preserving order.
func RoadmapByStatus(items []RoadmapItem, status string) []RoadmapItem {
	var out []RoadmapItem
	for _, it := range items {
		if it.Status == status {
			out = append(out, it)
		}
	}
	return out
}
```

In `internal/nav/overview.go`'s `viewOverview`, insert the Roadmap panel into the `sections` slice **between** the ranked panel and the Inbox panel, only when non-empty. Find where `viewOverview` builds its `sections` (ranked panel, then Inbox panel, then hintLine) and change it so the middle is:

```go
	sections := []string{panel(w, title, strings.TrimRight(rb.String(), "\n"))}
	if len(m.overview.Roadmap) > 0 {
		sections = append(sections, m.viewRoadmap(w))
	}
	sections = append(sections, panel(w, "Inbox", strings.TrimRight(ib.String(), "\n")))
	sections = append(sections, m.hintLine("↑↓ move · tab pane · ⏎ show link/path · esc back · q quit"))
	return strings.Join(sections, "\n")
```

(Adapt to the exact variable names already in `viewOverview` — `rb` is the ranked builder, `ib` the inbox builder, per the Plan 1a implementation. If the names differ, keep the existing builders and only insert the `if len(...Roadmap) > 0` block in the right place.)

- [ ] **Step 4: Generate the golden, inspect, confirm green**

Run: `go test ./internal/nav/ -run TestFlow_OverviewWithRoadmap_Golden -update`
Then: `cat internal/nav/testdata/overview_with_roadmap.golden`
Expected: a frame with the existing ranked + a **Roadmap** panel showing `Todo (2)` with its two items, `In Progress (1)` with its item, `Done · 1`, then the Inbox panel. No ANSI escapes. Eyeball it for sanity.

Then run both tests without `-update`:
Run: `go test ./internal/nav/ -run 'TestFlow_OverviewWithRoadmap_Golden|TestViewOverview_EmptyRoadmapOmitsTier' -v`
Expected: PASS.

Also confirm the **existing** golden is unchanged:
Run: `go test ./internal/nav/ -run TestFlow_PickerToOverview && git status --short internal/nav/testdata/picker_to_overview.golden`
Expected: PASS and **no diff** to the old golden (empty Roadmap omits the tier → frame unchanged).

- [ ] **Step 5: Full suite + commit**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/ internal/overview/ && go vet ./internal/nav/ ./internal/overview/`
Expected: `ok`; no gofmt output; vet clean.

```bash
git add internal/nav/overview.go internal/overview/overview.go internal/nav/flow_test.go internal/nav/testdata/overview_with_roadmap.golden
git commit -m "feat(nav): render Status-grouped Roadmap tier in overview

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: cmd/bridge wiring (`BRIDGE_PROJECT`)

**Files:**
- Modify: `cmd/bridge/nav.go`

- [ ] **Step 1: Wire `FetchRoadmap` into `overview.Config`**

In `cmd/bridge/nav.go`, inside the `BuildOverview` closure's `overview.Config{...}` (added in Plan 1a), set `FetchRoadmap`:

```go
				FetchRoadmap: roadmapFetcher(),
```

(Place it where the Plan 1a literal had `FetchRoadmap: nil`.)

Add the helper functions to `cmd/bridge/nav.go`:

```go
// roadmapFetcher returns a FetchRoadmap callback for the board named by
// BRIDGE_PROJECT ("owner/number"), or nil when unset/malformed (roadmap tier
// disabled). The token comes from BRIDGE_PROJECT_TOKEN, falling back to
// GH_TOKEN — it must carry the `project` scope.
func roadmapFetcher() func(ctx context.Context) ([]overview.RoadmapItem, error) {
	owner, number, ok := parseProjectRef(os.Getenv("BRIDGE_PROJECT"))
	if !ok {
		return nil
	}
	tok := os.Getenv("BRIDGE_PROJECT_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	return func(ctx context.Context) ([]overview.RoadmapItem, error) {
		c := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		items, err := c.ListProjectV2Items(ctx, owner, number)
		if err != nil {
			return nil, err
		}
		out := make([]overview.RoadmapItem, 0, len(items))
		for _, it := range items {
			out = append(out, overview.RoadmapItem{
				Repo:   it.Repo,
				Title:  it.Title,
				URL:    it.URL,
				Status: it.Status,
			})
		}
		return out, nil
	}
}

// parseProjectRef parses "owner/number" (e.g. "freaxnx01/5").
func parseProjectRef(s string) (owner string, number int, ok bool) {
	owner, num, found := strings.Cut(s, "/")
	if !found || owner == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return owner, n, true
}
```

Add imports to `cmd/bridge/nav.go` as needed: `"strconv"` (new), `"strings"` (already present for nav.go's existing use), `forge`/`overview`/`os`/`context` (already imported from Plan 1a).

- [ ] **Step 2: Build + vet + full suite**

Run:
```bash
go build ./... && go vet ./... && gofmt -l cmd/bridge/ | grep -v '.worktrees/'
go test ./internal/forge/ ./internal/overview/ ./internal/nav/ ./cmd/bridge/
```
Expected: builds; vet clean; no gofmt output; tests `ok`.

- [ ] **Step 3: Manual smoke (live board)**

Run:
```bash
just build
BRIDGE_PROJECT=freaxnx01/5 BRIDGE_PROJECT_TOKEN="$(direnv exec ~/projects/repos/github/freaxnx01/private sh -c 'printf %s "$GH_TOKEN"')" bridge nav
```
Then: press `o` → confirm a **Roadmap** section appears with the board's Todo/In Progress items and a Done count, between "what matters now" and "Inbox". (If you get "overview unavailable: … scopes", the token lacks `project` scope.)
Expected: the live roadmap tier renders from "freaxnx01 Backlog".

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/nav.go
git commit -m "feat(bridge): wire Projects v2 roadmap tier (BRIDGE_PROJECT)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Full verification

**Files:** none.

- [ ] **Step 1: Gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # expect empty
go vet ./...                          # clean
go test -race ./...                   # all ok, incl. forge/overview/nav
```

- [ ] **Step 2: Golden stability**

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff (both goldens stable).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/forge/... ./internal/overview/... ./internal/nav/... ./cmd/bridge/...` (if installed). Expect clean for the new code. If not installed, note it.

- [ ] **Step 4: Report**

Report actual output for Steps 1-2 and the Task 4 live smoke. No success claims without output.

---

## Notes for the implementer

- **The roadmap token needs `project` scope.** `BRIDGE_PROJECT_TOKEN` (fallback `GH_TOKEN`). Without it the GraphQL query returns `INSUFFICIENT_SCOPES`, which surfaces as "overview unavailable: …" — that's correct error handling, not a bug.
- **Don't add value/effort to roadmap items** — option B is Status-only; weighting stays on the issues tier.
- **Don't break the existing `picker_to_overview.golden`** — empty `Roadmap` must omit the tier (the `if len(m.overview.Roadmap) > 0` guard), so the old frame is unchanged. Verify with the no-diff check in Task 3 Step 4.
- **GraphQL is user-level** (`user(login:)`); org-level boards (`organization(login:)`) are a future follow-up, out of scope.
- If you hit a blocker, find the fix and note it inline here for the next run.
```
