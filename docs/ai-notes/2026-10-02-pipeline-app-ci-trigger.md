# Pipeline App opt-in for bridge — agent pushes start no CI

**Status (2026-10-06):** partly wired; blocked on two operator inputs + one release decision.
**Context session:** #313 (MCP stateless transport) end-to-end.

## Problem

Agent-pipeline commits on bridge PRs are pushed with the workflow's `GITHUB_TOKEN`.
GitHub starts **no** workflow runs for those pushes, so the required checks
(`golangci-lint`, `govulncheck` — `main` branch protection) never report and the PR
sits `BLOCKED`. Seen on PR #316: head `e61f20e` (the self-fix commit) had zero check
runs; unblocked only by a manual empty commit (`3ca8fe1`) pushed with the user's PAT.

agent-workflow already solves this with the **Pipeline GitHub App**
(`freaxnx01-pipeline`, agent-workflow #364 / #430, `docs/PIPELINE-APP-SETUP.md`).
bridge had never opted in.

## Done

- **bridge PR #330 (merged, `6013ecf`)** — `.github/workflows/agent.yml` passes
  `PIPELINE_APP_ID` / `PIPELINE_APP_PRIVATE_KEY` through explicitly, and sets
  `pipeline-author-allowlist` to both `github-actions[bot]` and
  `freaxnx01-pipeline[bot]` (newline-separated). No-op until the secrets exist —
  the workflow falls back to `GITHUB_TOKEN`.
- **`PIPELINE_APP_PRIVATE_KEY`** set on `freaxnx01/bridge` (2026-10-02 06:38Z), piped
  from Passbolt resource `9288dd6f-beb6-414b-ad43-a4347ea3124a`
  ("GitHub App agent-workflow", PEM `freaxnx01-pipeline.2026-09-23.private-key.pem`).

## Open — in order

1. **App ID** — needed for `gh secret set PIPELINE_APP_ID -R freaxnx01/bridge`.
   Not recorded anywhere reachable: not in Passbolt, docs, or memory; `/apps/<slug>`
   404s for a PAT because the App is private. Get it from the App's settings page
   (user). Optionally add it to the Passbolt entry's description.
2. **Installation** — confirm `freaxnx01-pipeline` is installed on
   `freaxnx01/bridge` (GitHub → Settings → Applications → Configure → repository
   access). A PAT cannot list App installations.
3. **agent-workflow release decision** — bridge pins `@v2` (= `v2.0.7`). `v2.0.7`
   has the App token path for **opening** the PR, but **not** the #430 fix
   (`actions/checkout` with `token: ${{ steps.app_token.outputs.token || github.token }}`,
   on `main` at `agent-implement.yml` ~L436-448). Without it, later agent pushes
   (e.g. self-fix commits — exactly #316's case) are still `github-actions[bot]`
   and start no CI. Cutting a release moves `v2` for **every** consumer and `main`
   is far ahead of `v2.0.7` → needs explicit user approval.
4. **Verify end-to-end** after 1–3: dispatch any bridge issue, then
   `gh pr view <n> --json author,statusCheckRollup --jq '{author: .author.login, checks: (.statusCheckRollup|length)}'`
   → author `app/freaxnx01-pipeline`, checks non-zero; and a self-fix push also
   gets check runs.

## Blocker noticed at handoff

`gh` returned **HTTP 401 Bad credentials** on 2026-10-06 (secrets list, GraphQL).
The PAT used by `gh` (Passbolt 51026ce1, via the freaxnx01 `.envrc`) is likely
rotated/expired — fix `gh auth` before any of the steps above.

## Related, already done this session

- #313 stateless MCP transport — merged (PR #316), deployed to `bridge-mcp.service`
  (2026-10-01 22:33, `v2.9.0-139-g33da90d`), verified via Traefik.
- Filed #327 (per-request MCP logging), #328 (`tools.listChanged` on stateless).
- Spec coverage correction — PR #329 merged. #289 closed as duplicate of #285.
- #232 (mcp-remote hang): user retested from Desktop after the deploy (2026-10-02).
