# nav Ctrl+N create-repo (#129) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Ctrl+N` on the bridge-nav repo picker → name → forge×visibility → create on the forge + clone → open the new repo's dashboard.

**Architecture:** A two-step inline modal (`newRepoModal`) on the picker, mirroring the `n` new-worktree modal. nav creates via an injected `Config.CreateRepo` callback (stays forge-token-free). A shared `createAndClone` core is extracted in `cmd/bridge` so the `bridge create` CLI and the nav callback use one create+clone path.

**Tech Stack:** Go (Cobra, Bubble Tea/lipgloss, stdlib `testing`/`httptest`). Spec: `docs/superpowers/specs/2026-06-21-nav-ctrl-n-create-design.md`.

---

## File Structure

- **Modify** `cmd/bridge/create.go` — extract `createAndClone`; `runCreate` becomes a thin wrapper.
- **Modify** `cmd/bridge/create_test.go` — add a direct `createAndClone` test (existing CLI tests stay green).
- **Modify** `internal/nav/types.go` — `newRepoModal`/`repoModalStep`/`repoForgeChoices`, `Config.CreateRepo`, `repoCreatedMsg`, `repoModal` model field.
- **Modify** `internal/nav/data.go` — `createRepoCmd`.
- **Modify** `internal/nav/update.go` — picker `ctrl+n`, `updateRepoModal`, `repoCreatedMsg` handling, `updatePicker` modal guard.
- **Modify** `internal/nav/view.go` — `viewRepoModal`, `viewPicker` modal dispatch.
- **Modify** `internal/nav/*_test.go` — Update tests + a golden modal flow test.
- **Modify** `cmd/bridge/nav.go` — wire `CreateRepo` to `createAndClone`.

---

## Task 1: extract `createAndClone` in cmd/bridge

**Files:**
- Modify: `cmd/bridge/create.go`
- Test: `cmd/bridge/create_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/bridge/create_test.go`:

```go
func TestCreateAndClone_GitHub(t *testing.T) {
	// httptest GitHub forge that accepts the create POST.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"proj","visibility":"public","default_branch":"main",
			"html_url":"https://gh/freaxnx01/proj","ssh_url":"git@github.com:freaxnx01/proj.git",
			"owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")

	// a repos root with a github/<owner>/public dir so githubTargetDir resolves
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "github", githubOwner, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_REPOS_ROOT", root)

	var clonedURL, clonedTarget string
	orig := cloneFn
	cloneFn = func(sshURL, target string) error { clonedURL, clonedTarget = sshURL, target; return nil }
	defer func() { cloneFn = orig }()

	repo, ref, err := createAndClone(context.Background(), "proj", "github", false)
	if err != nil {
		t.Fatal(err)
	}
	if clonedURL != "git@github.com:freaxnx01/proj.git" {
		t.Errorf("cloned ssh = %q", clonedURL)
	}
	if repo.Name != "proj" || repo.Owner != "freaxnx01" || repo.Forge != "github" || repo.Visibility != "public" {
		t.Errorf("repo = %+v", repo)
	}
	if repo.Path != clonedTarget || repo.Path != filepath.Join(root, "github", githubOwner, "public", "proj") {
		t.Errorf("path = %q (clonedTarget %q)", repo.Path, clonedTarget)
	}
	if ref.HTMLURL != "https://gh/freaxnx01/proj" {
		t.Errorf("ref.HTMLURL = %q", ref.HTMLURL)
	}
}

func TestCreateAndClone_InvalidName(t *testing.T) {
	if _, _, err := createAndClone(context.Background(), "bad name", "github", true); err == nil {
		t.Errorf("invalid name should error")
	}
}
```

(`create_test.go` already imports `context`, `net/http`, `net/http/httptest`, `os`, `path/filepath`, `testing` — verify; add any missing. `BRIDGE_REPOS_ROOT` is the env `reposRoots()` reads; confirm the var name with `grep -n "BRIDGE_REPOS_ROOT\|func reposRoots" cmd/bridge/bases.go` and use the real one.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/bridge/ -run TestCreateAndClone -v`
Expected: FAIL — `createAndClone` undefined (returns 2 values today via runCreate; it doesn't exist yet).

- [ ] **Step 3: Extract `createAndClone`; make `runCreate` a wrapper**

In `cmd/bridge/create.go`, add `"github.com/freaxnx01/bridge/internal/core"` to imports, then replace the body of `runCreate` with a call into a new `createAndClone`:

```go
// createAndClone validates the name, creates the repo on the forge, clones it,
// and returns the resulting local repo plus the forge ref. Shared by the
// `bridge create` CLI and nav's Ctrl+N CreateRepo callback.
func createAndClone(ctx context.Context, name, forgeName string, private bool) (core.Repo, forge.RepoRef, error) {
	if !validRepoName(name) {
		return core.Repo{}, forge.RepoRef{}, fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", name)
	}
	vis := "private"
	if !private {
		vis = "public"
	}
	var ref forge.RepoRef
	var targetDir string
	switch forgeName {
	case "forgejo":
		dir, tok, err := forgejoTargetDir()
		if err != nil {
			return core.Repo{}, forge.RepoRef{}, err
		}
		if tok == "" {
			return core.Repo{}, forge.RepoRef{}, fmt.Errorf("no Forgejo token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return core.Repo{}, forge.RepoRef{}, createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	case "github":
		dir, tok, err := githubTargetDir(vis)
		if err != nil {
			return core.Repo{}, forge.RepoRef{}, err
		}
		if tok == "" {
			return core.Repo{}, forge.RepoRef{}, fmt.Errorf("no GitHub token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return core.Repo{}, forge.RepoRef{}, createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	default:
		return core.Repo{}, forge.RepoRef{}, fmt.Errorf("unknown forge %q (use forgejo or github)", forgeName)
	}
	if err := cloneFn(ref.SSHURL, targetDir); err != nil {
		return core.Repo{}, forge.RepoRef{}, fmt.Errorf("created %s but clone failed: %v\nclone manually: git clone %s %s",
			ref.HTMLURL, err, ref.SSHURL, targetDir)
	}
	return core.Repo{
		Name: ref.Name, Path: targetDir, Forge: forgeName, Owner: ref.Owner,
		Visibility: vis, DefaultBranch: ref.DefaultBranch, RemoteURL: ref.SSHURL,
	}, ref, nil
}

func runCreate(cmd *cobra.Command, name, forgeName string, public, asJSON bool) error {
	repo, ref, err := createAndClone(cmd.Context(), name, forgeName, !public)
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(cmd.OutOrStdout(), map[string]any{
			"name": repo.Name, "full_name": repo.Owner + "/" + repo.Name,
			"forge": forgeName, "private": !public, "path": repo.Path, "html_url": ref.HTMLURL,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created %s/%s (%s, %s) -> %s\n",
		repo.Owner, repo.Name, repo.Visibility, forgeName, repo.Path)
	return nil
}
```

(`createErr`, `forgejoTargetDir`, `githubTargetDir`, `cloneFn`, `validRepoName`, `emitJSON` already exist. `cmd.Context()` provides the ctx; if `runCreate`'s existing tests call it without a context-bearing cmd, `cmd.Context()` returns `context.Background()` by default — fine.)

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/bridge/ -run 'TestCreateAndClone|TestCreate' -v`
Expected: PASS — the new `createAndClone` tests and the existing `bridge create` CLI tests (which now exercise `createAndClone` through `runCreate`).

- [ ] **Step 5: Commit**

```bash
gofmt -l cmd/bridge/ | grep -v worktrees ; go vet ./cmd/bridge/
git add cmd/bridge/create.go cmd/bridge/create_test.go
git commit -m "refactor(create): extract createAndClone (shared by CLI + nav)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: nav modal state + Update

**Files:**
- Modify: `internal/nav/types.go`
- Modify: `internal/nav/data.go`
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`:

```go
func TestUpdatePicker_CtrlN_OpensRepoModal(t *testing.T) {
	m := initialModel(Config{
		CreateRepo: func(name, forge string, private bool) (core.Repo, error) {
			return core.Repo{Name: name, Path: "/r/" + name, Forge: forge}, nil
		},
	})
	m.pickerFocus = focusList
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if out.(Model).repoModal == nil {
		t.Fatal("ctrl+n should open the repo modal")
	}
}

func TestUpdatePicker_CtrlN_NoopWhenDisabled(t *testing.T) {
	m := initialModel(Config{}) // CreateRepo nil
	m.pickerFocus = focusList
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if out.(Model).repoModal != nil {
		t.Error("ctrl+n should be a no-op when CreateRepo is nil")
	}
}

func TestRepoModal_NameStep_EmptyNameErrs(t *testing.T) {
	m := initialModel(Config{})
	m.repoModal = &newRepoModal{}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := out.(Model).repoModal; got.step != repoModalName || got.err == "" {
		t.Errorf("empty name should stay on name step with an err: %+v", got)
	}
}

func TestRepoModal_NameThenForgeThenCreate(t *testing.T) {
	var gotName, gotForge string
	var gotPriv bool
	m := initialModel(Config{
		CreateRepo: func(name, forge string, private bool) (core.Repo, error) {
			gotName, gotForge, gotPriv = name, forge, private
			return core.Repo{Name: name, Path: "/r/" + name, Forge: forge}, nil
		},
	})
	m.repoModal = &newRepoModal{}
	// type "proj"
	for _, r := range "proj" {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(Model)
	}
	// enter -> forge step
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.repoModal.step != repoModalForge {
		t.Fatalf("after name, step = %d, want forge", m.repoModal.step)
	}
	// move to "GitHub · Public" (index 3) then create
	for i := 0; i < 3; i++ {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if !m.repoModal.creating || cmd == nil {
		t.Fatal("enter on forge step should set creating + return a Cmd")
	}
	// resolve the create cmd -> repoCreatedMsg -> dashboard
	out, _ := m.Update(cmd())
	got := out.(Model)
	if gotName != "proj" || gotForge != "github" || gotPriv != false {
		t.Errorf("create args = %q/%q/%v", gotName, gotForge, gotPriv)
	}
	if got.screen != screenDash || got.repo.Name != "proj" || got.repoModal != nil {
		t.Errorf("after create: screen=%d repo=%+v modal=%v", got.screen, got.repo, got.repoModal)
	}
}

func TestUpdate_RepoCreatedMsg_Error(t *testing.T) {
	m := initialModel(Config{})
	m.repoModal = &newRepoModal{step: repoModalForge, creating: true}
	out, _ := m.Update(repoCreatedMsg{err: errFake})
	got := out.(Model)
	if got.repoModal == nil || got.repoModal.creating || got.repoModal.err == "" {
		t.Errorf("error should keep modal open, clear creating, set err: %+v", got.repoModal)
	}
}
```

(`errFake` already exists in `update_test.go`; `core` and `tea` are imported.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestUpdatePicker_CtrlN|TestRepoModal|TestUpdate_RepoCreatedMsg' -v`
Expected: FAIL — `newRepoModal`/`repoModalForge`/`repoCreatedMsg`/`Config.CreateRepo`/`m.repoModal` undefined.

- [ ] **Step 3: Add the types + Config field + message**

In `internal/nav/types.go`:

```go
type repoModalStep int

const (
	repoModalName repoModalStep = iota
	repoModalForge
)

// newRepoModal is the inline Ctrl+N create-repo state (picker screen).
type newRepoModal struct {
	name     string
	step     repoModalStep
	sel      int // index into repoForgeChoices
	creating bool
	err      string
}

// repoForgeChoices are the forge×visibility options in display order.
var repoForgeChoices = []struct {
	label, forge string
	private      bool
}{
	{"Forgejo · Private", "forgejo", true},
	{"Forgejo · Public", "forgejo", false},
	{"GitHub · Private", "github", true},
	{"GitHub · Public", "github", false},
}

type repoCreatedMsg struct {
	repo core.Repo
	err  error
}
```

Add to the `Config` struct (after `Clone`):

```go
	// CreateRepo creates a repo on the named forge (forgejo|github) at the given
	// visibility, clones it, and returns the local repo. Nil disables Ctrl+N.
	CreateRepo func(name, forgeName string, private bool) (core.Repo, error)
```

Add the model field to the `Model` struct (near `modal *newWorktreeModal`):

```go
	repoModal *newRepoModal
```

- [ ] **Step 4: Add `createRepoCmd` (data.go)**

In `internal/nav/data.go`:

```go
// createRepoCmd runs the injected CreateRepo callback off the Update loop for
// the modal's selected name + forge×visibility choice.
func (m Model) createRepoCmd() tea.Cmd {
	create := m.cfg.CreateRepo
	ch := repoForgeChoices[m.repoModal.sel]
	name := strings.TrimSpace(m.repoModal.name)
	return func() tea.Msg {
		repo, err := create(name, ch.forge, ch.private)
		return repoCreatedMsg{repo: repo, err: err}
	}
}
```

(`data.go` already imports `strings`? — if not, add it; it imports `tea`.)

- [ ] **Step 5: Wire Update**

In `internal/nav/update.go`, add the modal guard at the top of `updatePicker` (line ~207):

```go
func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.repoModal != nil {
		return m.updateRepoModal(msg)
	}
	// … existing body …
```

Add the `ctrl+n` case in the `focusList` switch (next to `case "r":`/`case "o":`):

```go
	case "ctrl+n":
		if m.cfg.CreateRepo != nil {
			m.repoModal = &newRepoModal{}
		}
```

Add `updateRepoModal` (near `updateModal`):

```go
func (m Model) updateRepoModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.repoModal.creating {
		return m, nil // ignore keys while the create is in flight
	}
	if m.repoModal.step == repoModalName {
		switch msg.Type {
		case tea.KeyEsc:
			m.repoModal = nil
		case tea.KeyEnter:
			if strings.TrimSpace(m.repoModal.name) == "" {
				m.repoModal.err = "name required"
			} else {
				m.repoModal.err = ""
				m.repoModal.step = repoModalForge
			}
		case tea.KeyBackspace:
			if r := []rune(m.repoModal.name); len(r) > 0 {
				m.repoModal.name = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			m.repoModal.name += string(msg.Runes)
		}
		return m, nil
	}
	// repoModalForge
	switch msg.String() {
	case "esc":
		m.repoModal.step = repoModalName
	case "up", "k":
		m.repoModal.sel = clampInt(m.repoModal.sel-1, 0, len(repoForgeChoices)-1)
	case "down", "j":
		m.repoModal.sel = clampInt(m.repoModal.sel+1, 0, len(repoForgeChoices)-1)
	case "enter":
		m.repoModal.creating = true
		m.repoModal.err = ""
		return m, m.createRepoCmd()
	}
	return m, nil
}
```

Add the `repoCreatedMsg` case in the top-level message switch in `Update` (next to `wtCreatedMsg`):

```go
	case repoCreatedMsg:
		if msg.err != nil {
			if m.repoModal != nil {
				m.repoModal.creating = false
				m.repoModal.err = msg.err.Error()
			}
			return m, nil
		}
		m.repoModal = nil
		return m.enterDash(msg.repo)
```

- [ ] **Step 6: Run tests + commit**

Run: `go test ./internal/nav/ -run 'TestUpdatePicker_CtrlN|TestRepoModal|TestUpdate_RepoCreatedMsg' -v && go test ./internal/nav/`
Expected: PASS (new) and the full nav package still green.

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/types.go internal/nav/data.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): Ctrl+N create-repo modal — state + Update

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: nav View (modal render) + golden flow

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/flow_test.go` + `internal/nav/testdata/`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/flow_test.go`:

```go
func TestViewRepoModal_NameAndForgeSteps(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.repoModal = &newRepoModal{name: "proj"}
	nameFrame := m.viewPicker()
	if !strings.Contains(nameFrame, "New repo") || !strings.Contains(nameFrame, "name: proj") {
		t.Errorf("name step frame wrong:\n%s", nameFrame)
	}
	m.repoModal.step = repoModalForge
	m.repoModal.sel = 2 // GitHub · Private
	forgeFrame := m.viewPicker()
	for _, want := range []string{"New repo · proj", "Forgejo · Private", "GitHub · Private", "GitHub · Public"} {
		if !strings.Contains(forgeFrame, want) {
			t.Errorf("forge step missing %q:\n%s", want, forgeFrame)
		}
	}
}

func TestFlow_CtrlN_RepoModal_Golden(t *testing.T) {
	s := newSession(t, Config{
		CreateRepo: func(name, forge string, private bool) (core.Repo, error) {
			return core.Repo{Name: name, Path: "/r/" + name, Forge: forge}, nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.send(tea.KeyMsg{Type: tea.KeyCtrlN})
	for _, r := range "proj" {
		s.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	s.send(tea.KeyMsg{Type: tea.KeyEnter}) // -> forge step
	assertGolden(t, "ctrln_repo_modal_forge", s.frame())
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestViewRepoModal|TestFlow_CtrlN' -v`
Expected: FAIL — `viewPicker` doesn't render the modal yet (and the golden is missing).

- [ ] **Step 3: Implement `viewRepoModal` + dispatch**

In `internal/nav/view.go`, add the guard at the top of `viewPicker` (line ~70):

```go
func (m Model) viewPicker() string {
	if m.repoModal != nil {
		return m.viewRepoModal()
	}
	// … existing body …
```

Add `viewRepoModal` (near `viewModal`):

```go
func (m Model) viewRepoModal() string {
	mo := m.repoModal
	if mo.step == repoModalName {
		body := "name: " + mo.name + "_\n\n" + stMuted.Render("⏎ next · esc cancel")
		if mo.err != "" {
			body += "\n" + stBad.Render(mo.err)
		}
		return panel(m.width, "New repo", body)
	}
	var b strings.Builder
	for i, ch := range repoForgeChoices {
		if i == mo.sel {
			b.WriteString(stSel.Render(stAccent.Render("▸ ")+ch.label) + "\n")
		} else {
			b.WriteString("  " + stText.Render(ch.label) + "\n")
		}
	}
	if mo.creating {
		b.WriteString("\n" + stMuted.Render("⏳ creating…"))
	} else {
		b.WriteString("\n" + stMuted.Render("↑↓ pick · ⏎ create · esc back"))
	}
	if mo.err != "" {
		b.WriteString("\n" + stBad.Render(mo.err))
	}
	return panel(m.width, "New repo · "+mo.name, strings.TrimRight(b.String(), "\n"))
}
```

- [ ] **Step 4: Generate the golden, inspect, confirm**

Run: `go test ./internal/nav/ -run 'TestViewRepoModal' -v` (should PASS now).
Run: `go test ./internal/nav/ -run TestFlow_CtrlN_RepoModal_Golden -update`
Then: `cat internal/nav/testdata/ctrln_repo_modal_forge.golden` — confirm the forge-step modal: title `New repo · proj`, the 4 choices, the hint, no ANSI. Eyeball it.
Run without `-update`: `go test ./internal/nav/ -run TestFlow_CtrlN_RepoModal_Golden`
Expected: PASS. Also confirm existing goldens unchanged: `git status --short internal/nav/testdata/` shows only the new file.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/ && go vet ./internal/nav/`

```bash
git add internal/nav/view.go internal/nav/flow_test.go internal/nav/testdata/ctrln_repo_modal_forge.golden
git commit -m "feat(nav): Ctrl+N create-repo modal — view + golden

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: cmd/bridge wiring (`CreateRepo`)

**Files:**
- Modify: `cmd/bridge/nav.go`

- [ ] **Step 1: Wire the callback**

In `cmd/bridge/nav.go`, add to the `nav.Config{…}` literal (after `Clone`):

```go
			CreateRepo: func(name, forgeName string, private bool) (core.Repo, error) {
				repo, _, err := createAndClone(context.Background(), name, forgeName, private)
				return repo, err
			},
```

(`context`, `core` are already imported in `nav.go`.)

- [ ] **Step 2: Build + vet + full suite**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . | grep -v '.worktrees/'
go test ./cmd/bridge/ ./internal/nav/
```
Expected: builds; vet clean; no gofmt output; tests `ok`.

- [ ] **Step 3: Manual smoke (best-effort — creates a REAL repo)**

Run (creates + clones a real repo — use a throwaway name; deletes left to you):
```bash
just build
bridge nav   # then on the picker: focus the list (↓), Ctrl+N, type a name, pick GitHub · Private, ⏎
```
Confirm the modal flows name → forge pick → `⏳ creating…` → lands on the new repo's dashboard, and the repo exists on the forge + cloned under `github/<owner>/private/<name>`. (Needs a `repo`-scope token for the chosen forge.)
Expected: a created+cloned repo and the dashboard for it.

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/nav.go
git commit -m "feat(bridge): wire nav Ctrl+N CreateRepo to createAndClone

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Full verification

**Files:** none.

- [ ] **Step 1: Gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # empty
go vet ./...                          # clean
go test -race ./...                   # all ok
```

- [ ] **Step 2: Golden stability**

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff (goldens stable).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/... ./cmd/bridge/...` (if installed). Else note it; `go vet` is the gate.

- [ ] **Step 4: Report**

Report Steps 1-2 output + the Task 4 manual smoke. No success claims without output.

---

## Notes for the implementer

- **DRY:** `createAndClone` is the single create+clone path — the CLI and the nav callback both use it; don't duplicate forge/clone logic.
- **nav stays forge-token-free** — it only calls `m.cfg.CreateRepo`; all forge/token work is in `cmd/bridge`.
- **Forge set is Forgejo + GitHub** (ADO deferred — `bridge create` has no ADO path).
- **Modal is picker-scoped** (`m.repoModal`), distinct from the dash-scoped `m.modal` (worktree); the `updatePicker`/`viewPicker` guards mirror `updateDash`/`viewModal`.
- **`ctrl+n` lives in the `focusList` switch** (like `r`/`o`) — reachable once the list has focus.
- If you hit a blocker, find the fix and note it inline here.
```
