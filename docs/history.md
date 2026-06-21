# Bridge — History & Roadmap

How the tool evolved, and where it's going.

## History

Oldest → newest:

1. **`clrepo` (bash)** — the original repo-picker / agent-session launcher, a
   bash script.
2. **Rename to `bridge` (still bash)** — `clrepo` → `bridge`; same bash
   implementation, broader "bridge between you and your repos/agents" framing.
3. **Rewrite in Go (Linux + Windows PowerShell)** — reimplemented as a single
   static Go binary (`~/.local/bin/bridge`) plus a thin shell-function shim that
   handles the `cd:` / `exec:` / `noop` directives it emits. Cross-platform
   (Linux + a Windows PowerShell shim). Cobra CLI + `internal/` libraries; the
   frozen bash scripts were deleted at **v2.1.0** (Phase 4, #35). The Go binary
   is the only implementation now.
4. **`bridge nav` TUI** — an interactive Bubble Tea TUI: a repo picker → a
   per-repo dashboard (sessions, worktrees, branches, commits, git status, open
   issues) → a cross-repo **Overview** (a weighted "what matters now" tier, a
   GitHub Projects v2 **Status roadmap** tier, and a raw-capture inbox). Mobile
   capture rides on the Telegram **bridge-bot** (`/idea` → commit via the GitHub
   Contents API).
5. **Next: Bridge WebUI with Visualization** — a browser surface (PC + mobile)
   on top of a bridge REST API, with an architecture-scope **visualization**.
   Visual PoC: [`docs/design/bridge-poc2.html`](design/bridge-poc2.html).

## Roadmap

The cockpit is being reshaped into a **per-environment headless core + clients**
(TUI, Telegram bot, and a WebUI). Build order:

1. ✅ **Cross-repo overview** — weighted tiers + Projects v2 Status roadmap tier
   (TUI). *(merged)*
2. 🔭 **Capture / routing (bridge-bot)** — `/idea` capture shipped; **issue** and
   **roadmap** capture are the next slices.
3. ◻ **REST API** — the headless core exposed over HTTP.
4. ◻ **Bridge WebUI with Visualization** — PC + mobile web on the REST API; the
   architecture-scope visualizer (see PoC below).
5. ◻ **Session continuity** — smoother tmux ↔ mobile Remote-Control handoff.

### Bridge WebUI with Visualization (#4)

A browser front-end on the bridge REST API, reachable from PC and mobile
(personal via the homelab Traefik routing). Its centerpiece is an
**architecture-scope visualization** — see the interactive proof-of-concept at
[`docs/design/bridge-poc2.html`](design/bridge-poc2.html):

- **Two projections** of a repo's clean-architecture layers (DOMAIN →
  APPLICATION → INFRASTRUCTURE → PRESENTATION): an **Onion** view (concentric
  rings, core → outer) and a **Layer** view (horizontal bands), with an animated
  morph between them.
- **Components as blips** on the rings — each shows a **test-coverage ring**, an
  **open-issue badge**, and a **feature-touch** marker. Pan + zoom; click to
  drill in.
- **Dependency edges colored by direction** — inward (toward the core) = ok;
  **outward = an architecture violation** (red, dashed).
- A **feature "sweep" wedge** showing the active task's progress across the
  layers, and a live **agent indicator** (a pinging blip) on whichever component
  an agent is currently working.
- **Per-component drill-down** → git (branch/worktree, ahead/behind/dirty,
  recent commits), open issues (joined by a `component:<name>` label), and —
  for UI components — a **wireframe + data-flow** panel.

The PoC is a single self-contained HTML/SVG file with mock data standing in for
the resolver + git/issues providers; the WebUI productionizes it against the
real REST API (#3).
