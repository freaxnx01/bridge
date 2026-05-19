# Ralph Loop: Implement Open GitHub Issues in clrepo

You are working autonomously in a loop on the `clrepo` repository (a shell helper that opens code-server in the browser at a given repo on claude-dev). Each iteration is a cold start — no memory of previous runs. Orient yourself from disk before doing anything.

## Orient (do this first, every iteration)

1. Read `.ralph/PLAN.md` — the ordered issue list with dependency notes.
2. Read `.ralph/NOTES.md` — flagged blockers and decisions from prior iterations.
3. Run `git status` — confirm working tree is clean. If not, STOP and write "DIRTY WORKING TREE" to NOTES.md.
4. Run `git log --oneline -15` — see recent progress.
5. Run `gh auth status` — confirm gh CLI is authenticated. If not, STOP.

## Phase 1: Planning (only if PLAN.md has no issue entries yet)

1. Fetch open issues:
   `gh issue list --state open --limit 100 --json number,title,body,labels,url`

2. For each issue, parse the body for dependency markers (case-insensitive):
   - "Depends on #N"
   - "Blocked by #N"
   - "Requires #N"
   - "After #N"
   - "Needs #N"
   Multiple deps per issue allowed.

3. Build the dependency graph. Topologically sort so leaf issues (no open dependencies) come first. Closed dependencies don't count as blockers — verify with `gh issue view <N> --json state`.

4. Write `.ralph/PLAN.md`:

   ```
   # Plan

   <!-- Format: - [ ] #N Title (depends on: #X, #Y | none) -->

   - [ ] #3 Add --help flag (depends on: none)
   - [ ] #7 Support multiple workspaces (depends on: #3)
   ```

5. If a dependency cycle is detected, list those issues at the BOTTOM of PLAN.md with a `[CYCLE]` prefix and document the cycle in NOTES.md. Proceed with the resolvable issues.

6. Commit PLAN.md directly to main:
   ```
   git add .ralph/PLAN.md .ralph/NOTES.md
   git commit -m "ralph: initial plan from N open issues"
   git push origin main
   ```

7. STOP. Do not start implementing in the same iteration.

## Phase 2: Implementation (every subsequent iteration)

1. Pick the FIRST unchecked issue in PLAN.md whose dependencies are all checked `[x]` OR closed on GitHub. If none qualify, document the blockage in NOTES.md and STOP.

2. Read the issue: `gh issue view <N> --json title,body,labels,comments,url`

3. Ensure you're on main and up to date:
   `git checkout main && git pull origin main`

4. Create a branch `ralph/issue-<N>-<kebab-slug>` (slug = short kebab-case title, max 40 chars, lowercase, no special chars). Record the start timestamp:
   ```
   IMPL_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
   git checkout -b ralph/issue-<N>-<slug>
   ```

5. Implement the issue. Conventions for clrepo:
   - Shell project (Bash). Edit the relevant scripts.
   - Match existing style — quoting, error handling, `set -euo pipefail` if used elsewhere.
   - Keep changes minimal and scoped to the issue. No drive-by refactors.
   - **Conventional Commits required** — use `feat:`, `fix:`, `chore:`, `docs:`, etc. (see project `CLAUDE.md`).
   - **If you edit `clrepo.sh`:** bump `_CLREPO_VERSION` (near the top of the file) per semver — patch for fixes, minor for features, major for breaking changes — AND add a matching entry to `CHANGELOG.md` (Keep a Changelog format: new version, today's date, `Added`/`Changed`/`Fixed` section). Both changes go in the same commit as the code change.
   - If the issue is ambiguous and requires a product decision you can't make from the body, STOP and document the ambiguity in NOTES.md.

6. Add or update tests where applicable:
   - If the repo uses `bats`, add a `.bats` test.
   - If there's a `tests/` or `test/` directory, follow its pattern.
   - If there's no test framework, document this in NOTES.md the first time, add a smoke check (`bash -n script.sh`), and proceed.

7. Run checks and record what you ran:
   - `bash -n` on edited scripts (syntax) at minimum.
   - `shellcheck` on edited scripts if installed.
   - Existing test runner if one exists.
   Fix any failures before committing.

8. Commit (Conventional Commits — pick the right type: `feat`/`fix`/`docs`/`chore`/etc.):
   ```
   git add -A
   git commit -m "<type>: <issue title>

   Implements GitHub issue #<N>.
   <one-paragraph summary of what changed and why>

   Closes #<N>.

   Co-Authored-By: Claude <noreply@anthropic.com>"
   ```

9. Push and open a DRAFT PR. Record the PR-open timestamp:
   ```
   PR_OPENED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
   FILES_CHANGED=$(git diff --name-only main | wc -l)
   git push -u origin ralph/issue-<N>-<slug>
   gh pr create \
     --title "<type>: <issue title>" \
     --body "Closes #<N>.

   <Summary of changes>

   ## Verification
   - <command 1>: <result>
   - <command 2>: <result>

   ## Loop telemetry
   - Iteration: <N>
   - Branch: ralph/issue-<X>-<slug>
   - Files changed: <FILES_CHANGED>
   - Checks run: <comma-separated list>
   - Implementation started: <IMPL_START>
   - PR opened: <PR_OPENED>

   _Token usage and cost available in the Claude Code session's /cost output and the Anthropic usage dashboard._

   _Opened by Ralph Loop._" \
     --draft \
     --base main
   ```

10. Switch back to main and update bookkeeping:
    ```
    git checkout main
    ```
    - Check the box `[x]` for this issue in `.ralph/PLAN.md`.
    - Append to `.ralph/NOTES.md`: `iter <N>: PR opened for #<X> — <one-line summary> — <PR URL>`
    - Update `.ralph/state.json`.
    - `git add .ralph/ && git commit -m "ralph: progress update after #<N>" && git push origin main`

11. STOP. One issue per iteration.

## .ralph/state.json schema

```json
{
  "last_iteration": 7,
  "last_completed_issue": 12,
  "issues_done": [3, 7, 12],
  "issues_remaining": [15, 18],
  "prs_opened": [
    {"issue": 3, "pr_url": "https://github.com/.../pull/41"}
  ],
  "blockers": [],
  "updated_at": "2026-05-19T20:14:00Z"
}
```

## Hard rules

- ONE issue per iteration. Do not batch.
- NEVER force-push.
- NEVER touch main directly EXCEPT to commit `.ralph/` bookkeeping and the initial PLAN.md.
- NEVER close issues or merge PRs. Humans review and merge.
- NEVER edit other issues' branches or PRs.
- All PRs are opened as DRAFT.
- If pre-existing checks are red on main BEFORE you start, STOP and document "PRE-EXISTING RED MAIN" in NOTES.md.
- If `gh` commands fail with auth errors, STOP — do not try to re-auth.
- If you cannot proceed for any reason, document it in NOTES.md and STOP. Don't guess, don't fabricate.
- Do NOT report token counts or costs you cannot verify. Telemetry is limited to observable facts (timestamps, file counts, commands run).

## Completion criteria

When every issue in PLAN.md is checked `[x]` AND `git status` on main is clean AND `.ralph/state.json` shows `issues_remaining: []` — and only then — the work is done.
