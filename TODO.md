# Bridge TODO

## Bridge WebUI — Plan 2 (Svelte UI Components)

Plan 1 (backend + scaffold) is merged on `main` as of 2026-06-25.
Plan 2 covers the actual Svelte pages and components.

**Required gates before writing any component code:**

- [ ] Run `/ui-brainstorm` → get ASCII wireframes approved for:
  - Overview page (`/`): Radar + Word Cloud viz panel, Tier list, Capture bar
  - Repo dashboard (`/repos/:owner/:name`): Sessions, Worktrees, Branches, Git status, Issues, ArchViz stub
- [ ] Run `/ui-flow` → get Mermaid state diagrams approved (Svelte store states + transitions)
- [ ] Write Plan 2 (`docs/superpowers/plans/YYYY-MM-DD-bridge-webui-plan2.md`)

**Then implement (Plan 2 scope):**

- [ ] Promote `bridge-overview-directions.html` (Radar + Word Cloud) to Svelte components
  - `<Radar {components} {deps} {activeFeature} {agentId} />`
  - `<WordCloud {components} />`
- [ ] Overview page (`web/src/routes/Overview.svelte`) — viz + tier list + capture bar
- [ ] Repo dashboard (`web/src/routes/RepoDashboard.svelte`) — panels + ArchViz stub
- [ ] Shared components: `RepoCard`, `CaptureModal`, `CreateRepoModal`, `SseProvider`
- [ ] Wire Svelte router (page routing for `/` and `/repos/:owner/:name`)

**Follow-ups (deferred from Plan 1):**

- [ ] Extend `ai-instructions` Go stack overlay with a `## Svelte (WebUI)` section
- [ ] Add `just test` recipe to run both `go test -race ./...` and `cd web && npm test -- --run`
- [ ] Clean-arch resolver for real ArchViz data (currently stub)
- [ ] `bridge serve` systemd unit for always-on homelab access
- [ ] Auth via Traefik middleware (BasicAuth / ForwardAuth) — no app changes needed

## Agent / Bot Integration (ideas captured 2026-06-24, GitHub issues #163–#171)

- [ ] #163 — bot `/ask <question>` (repo/issue summary)
- [ ] #164 — bot `/status` using `claude agents --json`
- [ ] #165 — session summary on tmux slot exit
- [ ] #166 — bot `/plan` (idea → mini-spec → GitHub issue)
- [ ] #167 — auto-label value/effort on issue capture
- [ ] #168 — WebUI "What next?" button (Claude-ranked recommendation)
- [ ] #169 — stale issue detector (>30 days, AI nudge)
- [ ] #170 — `bridge agents` nav/WebUI panel
- [ ] #171 — wire agent ping in Radar viz from real `claude agents` data
