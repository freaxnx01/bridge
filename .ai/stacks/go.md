[//]: # (Stack overlay — loaded together with .ai/base-instructions.md for Go projects)

# Go Stack Overlay

Applies on top of `.ai/base-instructions.md` for **Go** projects: command-line
tools and TUIs (the primary shape) as well as HTTP services. Targets the latest
stable Go toolchain via Go modules, built into a single static binary.

Use this stack for repos like `bridge` (Cobra CLI + Bubble Tea TUI + thin shell
shims), internal CLIs, automation daemons, and small HTTP services where the
deliverable is a Go binary.

---

## Tech Stack

Go (latest stable, pinned in `go.mod`; modules only) · [`spf13/cobra`](https://github.com/spf13/cobra) for CLI · [Charm](https://github.com/charmbracelet) `bubbletea`/`bubbles`/`lipgloss` for TUI · stdlib `net/http` with the Go 1.22+ `ServeMux` · `log/slog` · stdlib `testing` with hand-rolled fakes — **no** `testify`/`mockery`/`gomock` · `golangci-lint` · `govulncheck` · `just` + GitHub Actions · `goreleaser` only on request.

Full table and directory layout: [`.ai/references/go/tech-stack.md`](https://github.com/freaxnx01/ai-instructions/blob/main/.ai/references/go/tech-stack.md)

---

## Project Structure

- **`internal/` is the default.** Anything not meant to be imported by another
  module goes here — the compiler enforces the boundary. Promote a package to
  `pkg/` only when you are making a deliberate, supported public API promise.
- `cmd/<binary>/` holds the `main` package and Cobra command wiring **only** —
  no business logic. Logic lives in `internal/` packages the command calls.
- One package = one responsibility. A package that needs a comment to explain
  its grab-bag of contents is two packages.
- Package names are short, lower-case, no underscores or camelCase, and read
  well at the call site (`store.Open`, not `storepkg.OpenStore`).

Directory tree: [`.ai/references/go/tech-stack.md`](https://github.com/freaxnx01/ai-instructions/blob/main/.ai/references/go/tech-stack.md)

---

## Go Conventions

- `gofmt` (via `goimports`) is non-negotiable — formatting is never a review
  topic. CI fails on `gofmt -l` output.
- **Accept interfaces, return concrete types.** Define interfaces at the
  *consumer*, keep them small (1–3 methods), and don't export an interface
  "just in case."
- Prefer `:=` with the zero-value-aware short form; avoid `var` blocks except
  for sentinel errors and package-level constants.
- **No package-level mutable global state** (no mutable `var` singletons, no
  `init()` that wires dependencies). Pass dependencies explicitly via
  constructors — it's what makes the code testable without a DI framework.
- `context.Context` is the **first** parameter of any function that does I/O,
  blocks, or spawns goroutines (`ctx context.Context`). Never store a `Context`
  in a struct; never pass `nil` — use `context.TODO()` only as a temporary
  marker.
- Exported identifiers have doc comments that start with the identifier name.
- Keep zero values useful: a struct should be usable (or clearly not) without a
  constructor where reasonable.

---

## Error Handling

- Return errors, don't `panic`. `panic` is reserved for truly unrecoverable
  programmer bugs (nil that can never be nil), never for control flow and
  **never** in library code.
- Wrap with context using `%w`: `fmt.Errorf("open store %s: %w", path, err)`.
  The wrap message is lower-case, no trailing punctuation, and adds information
  the caller doesn't already have.
- Inspect wrapped errors with `errors.Is` (sentinel) / `errors.As` (typed) —
  never string-match on `err.Error()`.
- Sentinel errors are exported package vars: `var ErrNotFound = errors.New("…")`.
  Typed errors implement `error` and carry fields the caller needs.
- Errors flow **up to `main` / the Cobra command**, which is the single place
  that maps them to a user-facing message + process exit code. Lower layers
  don't call `os.Exit` or print to stderr.
- Never discard an error with `_ =` to silence the compiler/linter. If an error
  genuinely cannot matter (e.g. a deferred `Close` on a read-only file), handle
  it explicitly with a comment saying why.

---

## Concurrency

- Every goroutine has an owner responsible for its shutdown — no fire-and-forget
  `go func()` without a story for how it stops.
- Cancellation and deadlines propagate through `context.Context`; long-running
  loops select on `<-ctx.Done()`.
- Use `golang.org/x/sync/errgroup` to fan out work and collect the first error;
  use `sync.WaitGroup` only when you genuinely need none of the results.
- Protect shared state with `sync.Mutex`/`RWMutex` or confine it to a single
  goroutine and communicate via channels — don't mix the two for the same data.
- **The race detector is mandatory:** `go test -race ./...` runs in CI and
  must be green. A data race is a bug even if the test "passes" without `-race`.
- Don't reach for channels when a mutex is simpler, or a mutex when a single
  owning goroutine is simpler. Pick the least machinery that's correct.

---

## CLI Layer (Cobra)

- One Cobra command tree rooted in `cmd/<binary>/root.go`; subcommands are
  `*cobra.Command` values wired to thin `RunE` handlers that delegate to
  `internal/` packages.
- Use **`RunE`** (not `Run`) so errors return to the root for exit-code mapping;
  set `SilenceUsage`/`SilenceErrors` on the root and print the error once.
- Flags: long names with sensible shorthands; bind to a typed config struct, not
  loose package vars. Mark required flags with `MarkFlagRequired`.
- Shell completion goes through Cobra's `ValidArgsFunction` — register it once
  and let `cmd completion bash|zsh|fish|powershell` emit the per-shell scripts.
  Don't hand-write shell-specific completion logic.
- Exit codes: `0` success, `1` generic failure, reserve specific codes only when
  a consumer scripts against them (document them if so).

---

## TUI Layer (Charm / Bubble Tea)

Reach for a TUI only when interactive, stateful terminal UI genuinely beats
plain line-oriented CLI output — otherwise print and exit.

- Architecture is **Model-Update-View**: an immutable-ish `Model`, an
  `Update(msg) (Model, Cmd)` that returns a new model + commands, and a pure
  `View() string`. No I/O in `View`.
- Side effects (timers, I/O, subprocess) are `tea.Cmd`s returning `tea.Msg`s —
  never block the `Update` loop.
- Styling lives in `lipgloss` styles defined once as package vars, not inlined
  ANSI. Compose with `bubbles` widgets (list, table, textinput, spinner) rather
  than re-implementing them.
- Keep the model testable: `Update` is a pure function of `(model, msg)`, so
  drive it directly in tests without a terminal.
- Degrade gracefully when not a TTY (no `os.Stdin` interactivity in pipes/CI) —
  detect and fall back to non-interactive output.

---

## HTTP Services

For repos whose deliverable is an HTTP service (secondary to CLI/TUI):

- Default to the **standard library** `net/http` with the Go 1.22+ `ServeMux`
  pattern syntax (`mux.HandleFunc("GET /items/{id}", …)`). Add `chi` only when
  you need composable middleware stacks or sub-routers — state the reason.
- Middleware is `func(http.Handler) http.Handler`; compose explicitly.
- Always run with timeouts: set `ReadHeaderTimeout`, `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout` on the `http.Server` — never the zero-value
  server in production.
- **Graceful shutdown:** listen for `SIGINT`/`SIGTERM`, call `srv.Shutdown(ctx)`
  with a bounded context, drain in-flight requests.
- Handlers stay thin: parse/validate at the boundary, delegate to an
  `internal/` service, map domain errors to status codes in one place.
- 12-factor: bind to a `$PORT`/configured address, log to stdout, config from
  env (see base `12-Factor App Compliance`).

---

## Configuration

- 12-factor: configuration comes from **environment variables** (and Cobra flags
  for CLIs), never from committed config files with secrets.
- Resolve config once at startup into a single typed `Config` struct; pass it
  down explicitly. No scattered `os.Getenv` calls deep in the code.
- Precedence (highest first): explicit flag → environment variable → built-in
  default. Document each setting's env var name.
- Secrets (tokens, keys) come from the environment or a secrets manager only —
  never logged, never in argv where another process can read them, never
  written to `.git`-tracked files.

---

## Logging & Observability

- Diagnostics use **`log/slog`** — structured, leveled. Configure a JSON handler
  in production / non-TTY, a text handler for local dev. Attach a base logger;
  pass it (or a context-carried logger) down, don't reach for a global.
- **User-facing CLI output** is not logging: write intended program output to
  `os.Stdout` and human notices/warnings to `os.Stderr` via `fmt.Fprintln` —
  keep it clean and unstructured, no log levels/timestamps in what the user
  reads.
- No `fmt.Println`/`log.Printf` debug statements in committed code — delete them
  or convert to a real `slog.Debug` call.
- Never log secrets, tokens, full request bodies, or credential values — even at
  debug level.

---

## Testing

Base TDD rules (tests first, never modify a test to pass, never stub logic to go
green, run the full suite after changes) live in `base-instructions.md`. For this stack:

- Tests are `*_test.go` beside the code — same package for white-box, `<pkg>_test`
  for black-box; **table-driven with `t.Run` subtests** is the default shape
- **Hand-rolled fakes only** — no `testify`, `mockery`, `gomock`, or any codegen mocks
- Isolate with `t.TempDir()` / `t.Setenv()` / `t.Cleanup()`; golden files under
  `testdata/` behind an `-update` flag guard
- Name tests `TestFunc_StateUnderTest_ExpectedBehavior`

**TUI testing is two-tier and both tiers gate CI.** Tier 1 drives the `Model`
in-process with Charm's `teatest` — the one sanctioned test helper beyond stdlib.
Tier 2 runs the built binary in a real PTY via tmux and asserts on `capture-pane`.

- **Never launch a TUI in the foreground.** It runs in alt-screen/raw mode and only
  exits on a quit key, so a non-detached launch **blocks the turn forever**. Always
  `tmux -L <socket> new-session -d`, and guarantee teardown with
  `trap '… kill-server' EXIT`.
- Isolate the socket, size the pane, sleep between `send-keys` and `capture-pane`,
  and drive from deterministic fixtures.

### Required after every change

- `gofmt -l .` produces no output
- `go vet ./...` clean
- `golangci-lint run` clean
- `go test -race ./...` passes the **full** suite, not just the new test

Layout rules, the table-driven example, and the full tmux recipe:
[`.ai/references/go/testing.md`](https://github.com/freaxnx01/ai-instructions/blob/main/.ai/references/go/testing.md)

---

## Versioning (stack binding)

Base rules (SemVer, Conventional Commits → bump mapping, `git-cliff`, tag on
`main`) live in `base-instructions.md`. For this stack:

- The single source of version truth is the **git tag** (`vMAJOR.MINOR.PATCH`).
  There is no hand-edited version constant.
- The version is injected at build time via linker flags into `main`:

  ```text
  go build -ldflags "\
    -X main.version=$(git describe --tags --always --dirty) \
    -X main.commit=$(git rev-parse --short HEAD) \
    -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ```

  `var version = "dev"` in `main` is the fallback for `go run` / un-stamped
  builds. A `--version` flag / `version` subcommand prints all three.
- Do not duplicate the version in a `const`, a `VERSION` file, or `go.mod` —
  the tag plus ldflags is the one place.

---

## Essential Commands

```bash
# Build / install (prefer the project justfile so ldflags are stamped)
just build                              # build + install with version injection
go build ./...                          # compile everything
go run ./cmd/<binary> [args]            # run without installing

# Static checks (all gate CI)
gofmt -l .                              # MUST be empty
go vet ./...
golangci-lint run                       # aggregated linters
govulncheck ./...                       # known-vulnerability scan

# Tests
go test ./...                           # full suite
go test -race -cover ./...              # race detector + coverage (CI default)
go test ./internal/<pkg> -run TestXxx   # single package / single test
go test ./... -update                   # refresh golden files (where supported)

# Dependencies
go mod tidy                             # sync go.mod/go.sum to imports
go mod verify                           # verify module checksums
go get -u ./... && go mod tidy          # upgrade (ask before bumping majors)

# Cross-compile (static binary)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/<binary>
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/<binary>
```

Prefer the project `justfile` over raw `go build` for anything that ships — it
carries the ldflags version stamping. Invoke `just`, not `make`, where both
exist.

---

## Build & Release

- Default to **`CGO_ENABLED=0`** for a static, portable binary unless a
  dependency genuinely needs cgo.
- Version/commit/date are stamped via `-ldflags -X` (see Versioning). Add
  `-trimpath` for reproducible builds.
- Cross-compile via `GOOS`/`GOARCH`; the build matrix lives in the `justfile`
  and/or CI.
- Use `goreleaser` for multi-platform release archives + checksums **only** when
  the project actually ships such artifacts, and only when the user asks — don't
  add it speculatively.

---

## Security

Base security rules live in `base-instructions.md`. For this stack:

- `govulncheck ./...` runs in CI and **fails the build** on a known vulnerability
  in a reachable code path.
- `go mod verify` and committed `go.sum` guarantee dependency integrity; review
  diffs to `go.sum` in PRs.
- Validate all external input (flags, env, request bodies, file contents) at the
  boundary before it reaches domain logic.
- Never read secrets from argv (visible in `ps`); use env or stdin. Use
  `git -c credential.helper=…` style inline credentials rather than persisting
  tokens to `.git/config`.
- Keep the dependency tree small — every direct dependency is a review decision.
  Remove unused modules with `go mod tidy`.
- For HTTP services: HTTPS only, set security response headers, enforce request
  timeouts (see HTTP Services).

---

## Key Dependencies (defaults — discuss before swapping)

| Module | Purpose | Notes |
|---|---|---|
| `github.com/spf13/cobra` | CLI command tree + completion | Root wiring in `cmd/<binary>` only |
| `github.com/charmbracelet/bubbletea` | TUI runtime (Model-Update-View) | Only when an interactive TUI is justified |
| `github.com/charmbracelet/bubbles` | TUI widgets | list / table / textinput / spinner |
| `github.com/charmbracelet/lipgloss` | Terminal styling | Styles as package vars, not inline ANSI |
| `log/slog` (stdlib) | Structured logging | No third-party logger (`zap`, `zerolog`) without asking |
| `net/http` (stdlib) | HTTP server/client | `chi` only when middleware/sub-routers justify it |
| `golang.org/x/sync/errgroup` | Bounded concurrent fan-out | Preferred over hand-rolled `WaitGroup` + error plumbing |
| `testing` (stdlib) | Unit + table tests | Hand-rolled fakes; **no** testify/mockery/gomock |

---

## CI/CD (GitHub Actions outline)

Pipeline stages: `setup → fmt/vet → lint → test(-race) → vuln → build`.

```yaml
jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod      # single source for the Go version
          cache: true                  # module + build cache
      - run: test -z "$(gofmt -l .)"
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v6
      - run: go test -race -cover ./...
      - run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
      - run: go build ./...            # build all binaries
```

- Pin `go-version-file: go.mod` so the toolchain version has one source.
- Cache modules/build keyed on `go.sum`.
- Cross-platform binaries (Windows/macOS): add a build matrix job; there may be
  no Windows runner for behavioural tests — exercise those paths manually and
  say so in the PR.

---

## Project Scaffold Checklist (Go)

- [ ] `go.mod` with the module path and a pinned `go 1.x` line
- [ ] `cmd/<binary>/` (main + Cobra root) and `internal/` library layout
- [ ] `.golangci.yml` committed; `golangci-lint run` clean
- [ ] `var version = "dev"` in `main`, stamped via `-ldflags -X` at build
- [ ] `justfile` with `build` (ldflags), `test`, `lint`, cross-compile recipes
- [ ] At least one `*_test.go` table-driven test with hand-rolled fakes, green under `-race`
- [ ] `testdata/` + `-update` convention if golden files are used
- [ ] `CHANGELOG.md` with `[Unreleased]` section
- [ ] `cliff.toml` for `git-cliff`
- [ ] `.gitignore` includes built binaries, `dist/`, coverage output, `.worktrees/`
- [ ] `.github/workflows/` with fmt/vet/lint/test-race/govulncheck/build
- [ ] `.github/copilot-instructions.md`, `CLAUDE.md`, `SKILL.md` regenerated from base + this overlay
- [ ] Branch protection on `main`

---

## Agent Guardrails (stack-specific additions)

In addition to the base guardrails:

- Do not add a Go module without asking — every `go.mod` change is a deliberate
  decision; run `go mod tidy` after.
- Do not change the `go 1.x` line in `go.mod` or the toolchain version.
- Do not introduce a third-party logging (`zap`, `zerolog`), assertion
  (`testify`), or mocking (`mockery`, `gomock`) library — stdlib + hand-rolled
  fakes is the default.
- Do not add an HTTP router until stdlib `net/http` is provably insufficient.
- Do not add package-level mutable global state or dependency-wiring `init()`s.
- Do not call `os.Exit` or print to stderr below the `main`/command layer —
  return errors.
- Do not bypass the `justfile` ldflags for anything that ships (leaves the
  version unstamped).
- Do not commit built binaries, secrets, or credential files.
- Never disable a linter with `//nolint` to silence a warning — fix the code (a
  rare justified suppression carries an explanation comment).

### Never generate (this stack)

- `panic(...)` for control flow or in library code (return an error)
- Ignored errors via `_ = someCall()` to satisfy the compiler/linter
- `fmt.Println`/`log.Printf` debug statements in committed code
- `interface{}` / `any` outside genuine boundaries (JSON, `reflect`, generics)
- Package-level mutable `var` singletons or DI-wiring `init()` functions
- A `Context` stored in a struct field, or a `nil` context passed to a callee
- Goroutines with no shutdown/cancellation story (`go func(){...}()` and forget)
- Third-party testify/mockery/gomock/zap/zerolog imports added silently
- Tests modified to pass — fix the implementation
- Hardcoded return values, fake results, or stub logic to satisfy a test
- Silently swallowed errors to make a test green
- `//nolint` / `//go:build ignore` hacks to dodge lint or build failures
- Commented-out code blocks — delete them, git has history
- A hand-edited version constant or `VERSION` file (the git tag + ldflags is the source)

---

## UI workflow — stack-specific hints

Phase order and gates are defined in `base-instructions.md`. The UI workflow
applies to **CLI ergonomics and TUI screens**, not just web/mobile UIs. For Go:

- **Phase 1 (wireframe):** for a TUI, sketch the screen regions in ASCII
  (header, list/table body, status/help line, input field). For a plain CLI,
  sketch the command's stdout layout and the `--help` output.
- **Phase 2 (flow):** map TUI states to `bubbletea` model states and the
  messages that transition between them (Mermaid state diagram); for a command,
  map flags/subcommands to outcomes and exit codes.
- **Phase 3 (build):** shell first (model + empty `View`), then `Update`
  transitions, then `tea.Cmd` side effects, then `lipgloss` polish (colours,
  borders, help text). For a CLI: argument parsing → core call → output
  formatting → error/exit-code mapping.
- **Phase 4 (review):** `Update` is a pure, testable function; no I/O in `View`;
  graceful non-TTY fallback; completion registered via `ValidArgsFunction`;
  errors surface with useful exit codes; `--help` reads cleanly.
