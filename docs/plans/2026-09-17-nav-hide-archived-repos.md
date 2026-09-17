# nav: hide archived repos in the repo picker — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A local clone of a repo that was archived upstream stops appearing in the `bridge nav` repo picker, with `ctrl+a` to reveal archived rows on demand.

**Architecture:** The forge clients currently discard archived repos inside `ListRepos`, which is exactly the information nav needs — so they stop filtering and set `RepoRef.Archived` instead, while the two callers that relied on the filtering (the `list_repos` MCP tool and nav's clone-on-select rows) filter explicitly. `remote.Refresh` then carries archived refs into `~/.cache/bridge/remote.list`, and nav turns them into a `repoRowKey` set that `visibleRepos` subtracts from the picker rows.

**Tech Stack:** Go (stdlib `net/http`, `httptest`), Bubble Tea / Lip Gloss for the TUI, stdlib `testing` with hand-rolled fakes. No new dependencies.

**Spec:** [`docs/specs/2026-09-17-nav-hide-archived-repos-design.md`](../specs/2026-09-17-nav-hide-archived-repos-design.md)

## Global Constraints

- **No new Go modules.** Everything here is stdlib plus packages already imported.
- **No `testify`/`mockery`/`gomock`** — hand-rolled fakes and `httptest` only, as the existing tests in these packages already do.
- **Table-driven with `t.Run` subtests** is the default test shape; name tests `TestFunc_StateUnderTest_ExpectedBehavior`.
- **ADO is out of scope.** `internal/forge/ado.go` is not touched in any task.
- **Case-insensitive repo identity** is `strings.ToLower(forge + "\x00" + owner + "\x00" + name)` — the existing `repoRowKey` formula in `internal/nav/format.go:56`. Never introduce a second identity formula.
- **Fail open:** any missing, unreadable or stale archived information must leave rows *visible*.
- After every task: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

### Task 1: Forge clients report archived instead of hiding it

`ListRepos` on GitHub, Forgejo and GitLab drops archived repos today. That decision belongs to the caller, and nav cannot recover information the client threw away. This task inverts it.

**This task intentionally changes an existing test's contract.** `TestForgejoListRepos` currently asserts that an archived repo is filtered *out*. That assertion encodes the behaviour being replaced, so it is rewritten to assert the new contract — the archived repo comes back with `Archived: true`. This is a deliberate contract change stated in the spec, not a test bent to make code pass; Task 2 adds the test that keeps the user-visible behaviour of `list_repos` intact.

**Files:**
- Modify: `internal/forge/github.go:332-359` (`ListRepos`)
- Modify: `internal/forge/forgejo.go:313-335` (`ListRepos`)
- Modify: `internal/forge/gitlab.go:59-80` (`ListRepos`)
- Test: `internal/forge/github_test.go` (`TestGithubListRepos`, new case)
- Test: `internal/forge/forgejo_test.go` (`TestForgejoListRepos`, rewritten assertion)
- Test: `internal/forge/gitlab_test.go` (`TestGitlabListRepos`, new case)

**Interfaces:**
- Consumes: nothing.
- Produces: `forge.RepoRef.Archived` is `true` for archived repos returned by the GitHub, Forgejo and GitLab `ListRepos`. The field already exists (`internal/forge/client.go:83`, `json:"archived,omitempty"`), so no type changes.

- [ ] **Step 1: Write the failing tests**

In `internal/forge/github_test.go`, add this test after `TestGithubListRepos`:

```go
func TestGithubListRepos_ArchivedReturnedWithFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
          {"name":"bridge","visibility":"public","owner":{"login":"freaxnx01"}},
          {"name":"FlowHub-CAS-AISE","archived":true,"visibility":"public","owner":{"login":"freaxnx01"}}
        ]`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	repos, err := c.ListRepos(context.Background(), "freaxnx01")
	if err != nil {
		t.Fatal(err)
	}
	// Archived repos are no longer dropped here — callers decide. nav needs
	// them to recognise an archived local clone (#292).
	if len(repos) != 2 {
		t.Fatalf("want 2 repos (archived included), got %d: %+v", len(repos), repos)
	}
	if repos[0].Archived {
		t.Errorf("active repo must not be flagged archived: %+v", repos[0])
	}
	if !repos[1].Archived {
		t.Errorf("archived repo must carry Archived=true: %+v", repos[1])
	}
}
```

In `internal/forge/forgejo_test.go`, replace the two assertion blocks at the end of `TestForgejoListRepos` (the `if len(repos) != 1 …` check and the `for _, r := range repos` loop that fails on `archived-repo`) with:

```go
	// The fixture holds one active and one archived repo. Both come back; the
	// archived one is flagged rather than dropped (#292).
	if len(repos) != 2 {
		t.Fatalf("want 2 repos (archived included), got %d: %+v", len(repos), repos)
	}
	if repos[0].Forge != "forgejo" || repos[0].Name != "fj" || repos[0].Visibility != "public" || repos[0].Archived {
		t.Errorf("repo[0]: %+v", repos[0])
	}
	if repos[1].Name != "archived-repo" || !repos[1].Archived {
		t.Errorf("archived repo must carry Archived=true: %+v", repos[1])
	}
```

In `internal/forge/gitlab_test.go`, add this test after `TestGitlabListRepos`:

```go
func TestGitlabListRepos_ArchivedReturnedWithFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"glrepo","visibility":"public"},{"name":"glold","archived":true,"visibility":"public"}]`))
	}))
	defer srv.Close()
	c := NewGitlabClient("tok", srv.URL)
	repos, err := c.ListRepos(context.Background(), "freaxnx01")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("want 2 repos (archived included), got %d: %+v", len(repos), repos)
	}
	if !repos[1].Archived {
		t.Errorf("archived repo must carry Archived=true: %+v", repos[1])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/forge/ -run 'ListRepos' -v`
Expected: FAIL — `want 2 repos (archived included), got 1` in all three tests.

- [ ] **Step 3: Implement — stop filtering, set the flag**

In `internal/forge/github.go`, inside `ListRepos`'s `for _, r := range raw` loop, delete:

```go
		if r.Archived {
			continue
		}
```

and add `Archived: r.Archived,` to the `RepoRef` literal, after `UpdatedAt: r.UpdatedAt,`:

```go
		out = append(out, RepoRef{
			Forge:         "github",
			Owner:         o,
			Name:          r.Name,
			DefaultBranch: r.DefaultBranch,
			Description:   r.Description,
			Topics:        r.Topics,
			Visibility:    r.Visibility,
			HTMLURL:       r.HTMLURL,
			SSHURL:        r.SSHURL,
			UpdatedAt:     r.UpdatedAt,
			Archived:      r.Archived,
		})
```

In `internal/forge/forgejo.go`, inside `ListRepos`, delete the same three-line `if r.Archived { continue }` block and add `Archived: r.Archived,` to the `RepoRef` literal it builds.

In `internal/forge/gitlab.go`, inside `ListRepos`, delete the same three-line block and add `Archived: r.Archived,` to the `RepoRef` literal.

Update the doc comment above `GithubClient.ListRepos` by appending one sentence to the existing comment:

```go
// Archived repos are returned with Archived set rather than filtered out, so
// callers can decide — nav needs them to recognise an archived local clone.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/forge/ -run 'ListRepos' -v`
Expected: PASS for all `ListRepos` tests.

- [ ] **Step 5: Run the full package suite**

Run: `go test -race ./internal/forge/`
Expected: PASS. (`./...` will still fail at this point — Task 2 restores the MCP contract. That is expected between tasks.)

- [ ] **Step 6: Commit**

```bash
git add internal/forge/
git commit -m "refactor(forge): return archived repos with a flag instead of dropping them

ListRepos decided for its callers that archived repos are invisible, which
threw away exactly what nav needs to recognise an archived local clone.
Callers now filter explicitly.

Refs #292"
```

---

### Task 2: `list_repos` MCP tool keeps filtering archived

Task 1 widened what `ListRepos` returns, so without this task the `list_repos` MCP tool starts leaking archived repos to clients — a user-visible regression in a published tool contract.

**Files:**
- Modify: `internal/mcp/tools_read.go:146-181` (`handleListRepos`)
- Test: `internal/mcp/tools_read_test.go` (new test)

**Interfaces:**
- Consumes: `forge.RepoRef.Archived` from Task 1.
- Produces: nothing new. `listReposOutput.Repos` contains no archived refs, exactly as before Task 1.

- [ ] **Step 1: Write the failing test**

In `internal/mcp/tools_read_test.go`, add after `TestHandleListRepos_AggregatesDefaultOwners`:

```go
func TestHandleListRepos_ArchivedOmitted(t *testing.T) {
	gh := newFakeFull("github")
	gh.repos = []forge.RepoRef{
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
		{Forge: "github", Owner: "freaxnx01", Name: "FlowHub-CAS-AISE", Archived: true},
	}
	clients := map[string]*fakeFull{"github": gh}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	// The forge clients stopped filtering (#292); the tool contract did not.
	if len(out.Repos) != 1 || out.Repos[0].Name != "bridge" {
		t.Fatalf("archived repo must not reach the tool output: %+v", out.Repos)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/mcp/ -run TestHandleListRepos_ArchivedOmitted -v`
Expected: FAIL — `archived repo must not reach the tool output` with 2 repos.

- [ ] **Step 3: Implement the filter**

In `internal/mcp/tools_read.go`, inside `handleListRepos`'s goroutine, replace:

```go
			mu.Lock()
			all = append(all, repos...)
			mu.Unlock()
			return nil
```

with:

```go
			// The forge clients return archived repos flagged rather than
			// filtered (#292); this tool's contract is active repos only.
			active := make([]forge.RepoRef, 0, len(repos))
			for _, r := range repos {
				if r.Archived {
					continue
				}
				active = append(active, r)
			}
			mu.Lock()
			all = append(all, active...)
			mu.Unlock()
			return nil
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/mcp/ -run TestHandleListRepos -v`
Expected: PASS for every `TestHandleListRepos_*`.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS. The tree is green again from here on.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/
git commit -m "fix(mcp): keep archived repos out of list_repos output

The forge clients no longer filter them, so the tool does.

Refs #292"
```

---

### Task 3: nav derives the archived key set from the remote cache

nav learns which repos are archived at the one seam where forge refs are in hand — `remoteRows` — and carries the result on the messages the picker already handles.

**Files:**
- Modify: `internal/nav/format.go` (add `refKey`)
- Modify: `internal/nav/data.go:86-106` (`loadRemoteCmd`, `remoteRows`, add `archivedKeys`), `internal/nav/data.go:343-359` (`refreshRemoteCmd`)
- Modify: `internal/nav/types.go:254-262` (`remoteMsg`, `remoteErrMsg`)
- Modify: `internal/nav/model.go:12-36` (`Model` fields)
- Modify: `internal/nav/update.go:35-52` (`remoteMsg` / `remoteErrMsg` handlers)
- Test: `internal/nav/data_test.go` (new tests)

**Interfaces:**
- Consumes: `forge.RepoRef.Archived` from Task 1.
- Produces:
  - `func refKey(r forge.RepoRef) string` — lower-cased `forge\x00owner\x00name`, identical in form to `repoRowKey`.
  - `func archivedKeys(refs []forge.RepoRef) map[string]bool` — `refKey` set of the archived refs; `nil` when none are archived.
  - `remoteMsg{rows []repoRow; archived map[string]bool}` and `remoteErrMsg{err error; rows []repoRow; archived map[string]bool}`.
  - `Model.archived map[string]bool` and `Model.showArchived bool`.
  - `remoteRows` no longer emits rows for archived refs.

- [ ] **Step 1: Write the failing tests**

In `internal/nav/data_test.go`, add:

```go
func TestArchivedKeys_MatchesRepoRowKeyIdentity(t *testing.T) {
	refs := []forge.RepoRef{
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
		{Forge: "github", Owner: "FreaxNx01", Name: "FlowHub-CAS-AISE", Archived: true},
	}
	keys := archivedKeys(refs)
	if len(keys) != 1 {
		t.Fatalf("want 1 archived key, got %d: %+v", len(keys), keys)
	}
	// The key must collide with the local row's key despite the owner casing,
	// which is the whole point of reusing repoRowKey's formula.
	local := repoRow{repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "flowhub-cas-aise"}}
	if !keys[repoRowKey(local)] {
		t.Errorf("archived key must match the local row key, got %+v", keys)
	}
}

func TestArchivedKeys_NoneArchived_ReturnsEmpty(t *testing.T) {
	keys := archivedKeys([]forge.RepoRef{{Forge: "github", Owner: "o", Name: "a"}})
	if len(keys) != 0 {
		t.Errorf("want no keys, got %+v", keys)
	}
}

func TestRemoteRows_ArchivedRefsDropped(t *testing.T) {
	rows := remoteRows([]forge.RepoRef{
		{Forge: "github", Owner: "o", Name: "active"},
		{Forge: "github", Owner: "o", Name: "old", Archived: true},
	})
	// An archived repo must never be offered as a clone-on-select row.
	if len(rows) != 1 {
		t.Fatalf("want 1 remote row, got %d: %+v", len(rows), rows)
	}
	if rows[0].remote == nil || rows[0].remote.Name != "active" {
		t.Errorf("wrong row kept: %+v", rows[0])
	}
}

func TestUpdate_RemoteMsg_StoresArchivedSet(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(remoteMsg{
		rows:     []repoRow{{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "a"}}},
		archived: map[string]bool{"github\x00o\x00old": true},
	})
	if got := out.(Model).archived; len(got) != 1 {
		t.Errorf("remoteMsg must store the archived set, got %+v", got)
	}
}

func TestUpdate_RemoteErrMsg_PartialStoresArchivedSet(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(remoteErrMsg{
		err:      errors.New("one forge down"),
		rows:     []repoRow{{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "a"}}},
		archived: map[string]bool{"github\x00o\x00old": true},
	})
	if got := out.(Model).archived; len(got) != 1 {
		t.Errorf("partial refresh must still store the archived set, got %+v", got)
	}
}
```

Add `"errors"` and, if not already imported in that file, `"github.com/freaxnx01/bridge/internal/core"` and `"github.com/freaxnx01/bridge/internal/forge"` to the test file's import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav/ -run 'ArchivedKeys|RemoteRows_Archived|StoresArchivedSet' -v`
Expected: FAIL to compile — `undefined: archivedKeys`, `unknown field archived in struct literal`.

- [ ] **Step 3: Implement**

In `internal/nav/format.go`, add below `repoRowKey`:

```go
// refKey is the case-insensitive forge+owner+name identity of a forge ref, in
// the same form as repoRowKey so a remote ref and its local clone collide.
func refKey(r forge.RepoRef) string {
	return strings.ToLower(r.Forge + "\x00" + r.Owner + "\x00" + r.Name)
}
```

Add `"github.com/freaxnx01/bridge/internal/forge"` to that file's imports if it is not already there.

In `internal/nav/data.go`, add below `remoteRows`:

```go
// archivedKeys returns the refKey identity of every archived ref, which the
// picker subtracts from its rows. Nil when nothing is archived, so an
// environment without archived repos carries no state at all.
func archivedKeys(refs []forge.RepoRef) map[string]bool {
	var out map[string]bool
	for _, r := range refs {
		if !r.Archived {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[refKey(r)] = true
	}
	return out
}
```

In the same file, make `remoteRows` skip archived refs — insert after `ref := refs[i]`:

```go
		if ref.Archived {
			// Never offer an archived repo as clone-on-select; it only feeds
			// archivedKeys.
			continue
		}
```

In `loadRemoteCmd`, change the success return to:

```go
		return remoteMsg{rows: remoteRows(c.Repos), archived: archivedKeys(c.Repos)}
```

In `refreshRemoteCmd`, change both returns:

```go
		if err != nil {
			// Refresh returns partial refs alongside the first error when one
			// forge fails but others succeed; keep those fresh rows.
			return remoteErrMsg{err: err, rows: remoteRows(refs), archived: archivedKeys(refs)}
		}
		return remoteMsg{rows: remoteRows(refs), archived: archivedKeys(refs)}
```

In `internal/nav/types.go`, replace the two message types:

```go
type remoteMsg struct {
	rows     []repoRow
	archived map[string]bool // refKey set of archived repos
}

// remoteErrMsg reports a failed remote refresh. rows carries any partial fresh
// rows that loaded before the failure (e.g. one forge 401'd while others
// succeeded); empty rows means a total failure that keeps the cached rows.
type remoteErrMsg struct {
	err      error
	rows     []repoRow
	archived map[string]bool // refKey set from the partial refs, if any
}
```

In `internal/nav/model.go`, add two fields to `Model` next to `forgeFilter`:

```go
	archived     map[string]bool // refKey set of archived repos, from the remote cache
	showArchived bool            // ctrl+a reveals archived rows; session-local, default false
```

In `internal/nav/update.go`, store the set in both handlers:

```go
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.archived = msg.archived
		m.remoteState = loadOK
		m = m.normalizeForgeFilter()
		return m, m.issueCountCmds(msg.rows)
	case remoteErrMsg:
		if len(msg.rows) > 0 {
			// Partial success: at least one forge loaded. Show the fresh rows
			// rather than discarding them; the cache would only be staler.
			m.remoteRepos = msg.rows
			m.archived = msg.archived
			m.remoteState = loadPartial
			m = m.normalizeForgeFilter()
			return m, m.issueCountCmds(msg.rows)
		}
```

Leave the total-failure branch below untouched: it keeps the previously loaded rows, so it must keep the previously loaded archived set too.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'ArchivedKeys|RemoteRows_Archived|StoresArchivedSet' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/nav/
git commit -m "feat(nav): derive the archived repo key set from the remote cache

Refs #292"
```

---

### Task 4: `visibleRepos` hides archived rows; `ctrl+a` toggles

**Files:**
- Modify: `internal/nav/update.go:266-280` (`visibleRepos`)
- Modify: `internal/nav/update.go:389-411` (`updatePicker`'s picker-global switch)
- Test: `internal/nav/update_test.go` (new tests)

**Interfaces:**
- Consumes: `Model.archived`, `Model.showArchived` (Task 3); `repoRowKey` (`format.go:56`); `clampInt`.
- Produces: `visibleRepos()` excludes archived rows unless `showArchived`; `ctrl+a` flips `showArchived` from any picker focus.

- [ ] **Step 1: Write the failing tests**

In `internal/nav/update_test.go`, add near the other `visibleRepos` / `ctrl+f` tests:

```go
// archivedModel has one active and one archived local clone, plus the archived
// key set nav would have built from the remote cache.
func archivedModel() Model {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"}},
		{label: "github/public/FlowHub-CAS-AISE", repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "FlowHub-CAS-AISE"}},
	}
	m.archived = map[string]bool{"github\x00freaxnx01\x00flowhub-cas-aise": true}
	return m
}

func TestVisibleRepos_ArchivedHiddenByDefault(t *testing.T) {
	m := archivedModel()
	rows := m.visibleRepos()
	if len(rows) != 1 || rows[0].repo.Name != "bridge" {
		t.Fatalf("archived local clone must be hidden, got %+v", rows)
	}
}

func TestVisibleRepos_ArchivedShownWhenToggled(t *testing.T) {
	m := archivedModel()
	m.showArchived = true
	if got := len(m.visibleRepos()); got != 2 {
		t.Errorf("showArchived must reveal the archived row, got %d rows", got)
	}
}

func TestVisibleRepos_EmptyArchivedSetHidesNothing(t *testing.T) {
	m := archivedModel()
	m.archived = nil // no cache / unreadable cache — fail open
	if got := len(m.visibleRepos()); got != 2 {
		t.Errorf("an empty archived set must hide nothing, got %d rows", got)
	}
}

func TestUpdatePicker_CtrlA_TogglesArchived(t *testing.T) {
	m := archivedModel() // pickerFocus == focusFilter (initial)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := out.(Model)
	if !got.showArchived {
		t.Fatal("ctrl+a should reveal archived rows")
	}
	if got.pickerFocus != focusFilter {
		t.Errorf("ctrl+a must not change focus, got %d", got.pickerFocus)
	}
	if len(got.visibleRepos()) != 2 {
		t.Errorf("revealed row count wrong: %+v", got.visibleRepos())
	}
	out2, _ := got.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if out2.(Model).showArchived {
		t.Error("ctrl+a should hide archived rows again")
	}
}

func TestUpdatePicker_CtrlA_WorksWhileTypingFilter(t *testing.T) {
	m := archivedModel()
	m.filter.SetValue("fl")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := out.(Model)
	if !got.showArchived {
		t.Error("ctrl+a should toggle even with filter text present")
	}
	if got.filter.Value() != "fl" {
		t.Errorf("ctrl+a must not be captured as filter text, got %q", got.filter.Value())
	}
}

func TestUpdatePicker_CtrlA_NoopWhenNothingArchived(t *testing.T) {
	m := archivedModel()
	m.archived = nil
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if out.(Model).showArchived {
		t.Error("ctrl+a with no archived repos should be a no-op")
	}
}

func TestUpdatePicker_CtrlA_KeepsSelectionInRange(t *testing.T) {
	m := archivedModel()
	m.showArchived = true
	m.pickerSel = 1 // the archived row
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := out.(Model)
	if got.pickerSel > len(got.visibleRepos())-1 {
		t.Errorf("selection %d out of range for %d rows", got.pickerSel, len(got.visibleRepos()))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav/ -run 'VisibleRepos_Archived|CtrlA' -v`
Expected: FAIL — archived rows still visible, `showArchived` never set.

- [ ] **Step 3: Implement the filter and the toggle**

In `internal/nav/update.go`, in `visibleRepos`, insert the archived filter between `disambiguateOwners` and the forge subfilter:

```go
func (m Model) visibleRepos() []repoRow {
	all := append([]repoRow{}, m.localRepos...)
	all = append(all, dedupRemoteRows(m.localRepos, m.remoteRepos)...)
	all = disambiguateOwners(all)
	if !m.showArchived && len(m.archived) > 0 {
		kept := make([]repoRow, 0, len(all))
		for _, r := range all {
			if m.archived[repoRowKey(r)] {
				continue
			}
			kept = append(kept, r)
		}
		all = kept
	}
	if m.forgeFilter != "" {
```

(the rest of the function is unchanged).

In `updatePicker`, add a case to the picker-global switch, directly after the `ctrl+f` case:

```go
	case "ctrl+a":
		// Picker-global like ctrl+f: the picker opens with the filter focused,
		// where a bare letter key would be typed instead of bound.
		if len(m.archived) == 0 {
			return m, nil
		}
		m.showArchived = !m.showArchived
		m.pickerSel = clampInt(m.pickerSel, 0, len(m.visibleRepos())-1)
		return m, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'VisibleRepos_Archived|CtrlA' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/nav/
git commit -m "feat(nav): hide archived repos in the picker, ctrl+a reveals them

Refs #292"
```

---

### Task 5: Mark revealed rows and advertise the key

Without this, a revealed archived row is indistinguishable from an active one and the key is undiscoverable.

**Files:**
- Modify: `internal/nav/view.go:286-292` (picker row render loop), `internal/nav/view.go:300-304` (hint line), `internal/nav/view.go:779-784` (add `repoArchivedTag` beside `repoIssueTag`)
- Test: `internal/nav/view_test.go` (new tests)

**Interfaces:**
- Consumes: `Model.archived`, `Model.showArchived`, `repoRowKey`, `stMuted`.
- Produces: `func (m Model) repoArchivedTag(r repoRow) string` — `"  " + stMuted.Render("archived")` for a revealed archived row, `""` otherwise.

- [ ] **Step 1: Write the failing tests**

In `internal/nav/view_test.go`, add:

```go
func TestViewPicker_ArchivedRowCarriesMarker(t *testing.T) {
	m := archivedModel()
	m.showArchived = true
	m.width, m.height = 120, 40
	out := m.View()
	if !strings.Contains(out, "FlowHub-CAS-AISE") {
		t.Fatalf("revealed archived row missing from view:\n%s", out)
	}
	if !strings.Contains(out, "archived") {
		t.Errorf("revealed archived row must carry an 'archived' marker:\n%s", out)
	}
}

func TestViewPicker_ArchivedHintShownOnlyWhenArchivedPresent(t *testing.T) {
	m := archivedModel()
	m.width, m.height = 120, 40
	if !strings.Contains(m.View(), "ctrl+a archived") {
		t.Errorf("hint must advertise ctrl+a when archived repos exist:\n%s", m.View())
	}

	none := archivedModel()
	none.archived = nil
	none.width, none.height = 120, 40
	if strings.Contains(none.View(), "ctrl+a archived") {
		t.Errorf("hint must stay clean when nothing is archived:\n%s", none.View())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav/ -run 'ViewPicker_Archived' -v`
Expected: FAIL — no `archived` marker, no `ctrl+a archived` hint.

- [ ] **Step 3: Implement the tag and the hint**

In `internal/nav/view.go`, add beside `repoIssueTag`:

```go
// repoArchivedTag marks a row the archived filter would normally hide, so a
// revealed row never looks like an ordinary one. Empty unless revealed.
func (m Model) repoArchivedTag(r repoRow) string {
	if !m.showArchived || !m.archived[repoRowKey(r)] {
		return ""
	}
	return "  " + stMuted.Render("archived")
}
```

In the picker row loop, change the tag line from:

```go
			tag := repoIssueTag(rows[i])
```

to:

```go
			tag := repoIssueTag(rows[i]) + m.repoArchivedTag(rows[i])
```

And extend the hint, directly after the existing `forgeSubfilterVisible` block:

```go
	if len(m.archived) > 0 {
		hint += " · ctrl+a archived"
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'ViewPicker_Archived' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite, including golden files**

Run: `go test -race ./...`
Expected: PASS. If a golden-file flow test fails purely because the hint line grew, refresh it with `go test ./internal/nav/ -update` and **inspect the diff** — only the hint line and archived rows may change. Any other difference means a real regression, not a stale golden.

- [ ] **Step 6: Commit**

```bash
git add internal/nav/
git commit -m "feat(nav): mark revealed archived rows and hint ctrl+a

Refs #292"
```

---

### Task 6: Document the behaviour

**Files:**
- Modify: `CHANGELOG.md` (the `## [Unreleased]` section)
- Modify: `README.md` (nav picker key documentation, beside the existing `ctrl+r` / `ctrl+n` picker keys)

**Interfaces:**
- Consumes: the finished behaviour from Tasks 1-5.
- Produces: nothing code-facing.

- [ ] **Step 1: Add the changelog entries**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md`, add:

```markdown
- `bridge nav`: the repo picker **hides repos archived on their forge**, including local clones of them — an archived repo can't be worked on, so it was pure noise in the list. **`ctrl+a`** reveals them (rendered muted with an `archived` marker) and hides them again; the hint only advertises the key when there is something archived to reveal. Archived state comes from the remote repo cache, so a missing, unreadable or stale cache, or a repo with no remote at all, hides nothing. (#292)
```

Add a `### Changed` section under `## [Unreleased]` (after `### Added`) with:

```markdown
- `forge.ListRepos` (GitHub, Forgejo, GitLab) now **returns archived repos with `Archived: true`** instead of silently dropping them, so callers decide. The `list_repos` MCP tool and nav's clone-on-select rows filter them explicitly, so both behave exactly as before. (#292)
```

- [ ] **Step 2: Document the key in the README**

`README.md` describes `bridge nav` as a prose paragraph (line 61), not a key list — do not add a new section or a bullet list. Append one sentence to that paragraph, right after the sentence ending "…clone on select) and a per-repo dashboard of tmux sessions and worktrees with async git-dirty status and remote sync (ahead/behind, refreshed by a background fetch).":

```markdown
Repos archived on their forge are hidden from the picker — including local clones of them — and `ctrl+a` toggles them back into view, muted and tagged `archived`.
```

- [ ] **Step 3: Verify the docs build clean**

Run: `npx --yes markdownlint-cli2 CHANGELOG.md README.md`
Expected: no errors. If `markdownlint-cli2` is unavailable offline, skip it and visually confirm the list formatting matches the surrounding entries.

- [ ] **Step 4: Run the full verification set**

Run:

```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
```

Expected: `gofmt -l .` prints nothing; the rest clean/PASS.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs: record archived-repo hiding in nav and the ListRepos change

Closes #292"
```

---

## Manual verification (after Task 6)

Not a substitute for the tests — a check that the wiring is real against the actual cache:

```bash
just build
bridge list -r --refresh          # repopulate remote.list with archived refs
bridge nav
```

Expect: `FlowHub-CAS-AISE` absent from the Repos list; the hint line ends with `· ctrl+a archived`; pressing `ctrl+a` brings the row back muted with an `archived` tag; pressing it again removes it.
