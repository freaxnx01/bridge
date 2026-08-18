Resume the **Bridge alias capture routing — target-side** implementation (this repo, `github.com/freaxnx01/bridge`).

**Artifact (approved, no-placeholder plan):**
`docs/plans/2026-08-18-bridge-alias-capture-routing-targetside.md` — read it first. It holds all 5 TDD tasks, locked decisions, and full test code.

**Phase:** plan written + user-approved. **Execution not yet started** — branch `feat/alias-capture-routing` is fresh off `main`, nothing implemented.

**Next step:** execute the plan with `superpowers:subagent-driven-development` — fresh subagent per task (1→5), spec+quality review between each, a whole-branch review, then open a PR to `main`. Verify with `go test ./...`, `golangci-lint run ./...`, and `govulncheck ./...` (required check). YAML via `go.yaml.in/yaml/v3` (already transitively in `go.sum` — no new module).

**Scope:** §B of the design — `.bridge.yaml` alias indexing on `core.Repo` (→ `GET /api/repos`), `core.ResolveAlias` (unknown→404, ambiguous→409, never silently picks), issue `body`, `alias`/`body` on the REST capture endpoints, and a `BRIDGE_API_TOKEN` bearer middleware on `/api/capture/*` (unset→open dev default; read endpoints never gated).

**Cross-repo context (FlowHub side is DONE):** `freaxnx01/flowhub#16` (Bridge alias routing) and `#17` (SSH.NET NU1903 pin + hadolint) are **merged** to flowhub `main`. `flowhub#18` (raise diff-scope guard 800→2000) is **open, awaiting CI — merge when green**. After this bridge PR merges, the operator still must: seed `.bridge.yaml` aliases in repos, deploy `bridge serve` reachable from CT 136 (GH/FORGEJO/BRIDGE_API tokens), and set FlowHub `Skills__Bridge__BaseUrl`/`Skills__Bridge__ApiToken`.
