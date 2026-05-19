# Ralph journal

iter 1 (2026-05-19): Phase 1 (Planning) — wrote PLAN.md from 7 open issues. Dep markers found: #3→#4, #5→#3,#4. No cycles. Effort scores (lower=easier=first):

- #6=0  (no flag/config keywords, short body)
- #7=+2 (flag keywords)
- #4=+4 (flag+length)
- #3=+4 (flag+length, layer 1 after #4)
- #5=+4 (flag+length, layer 2 after #3)
- #8=+8 (epic label +4, flag, cross-platform)
- #9=+9 (enhancement+long body +3, flag, length, xplat keywords)

Surprise: #9 outscored #8 by 1 point because #9's body is long and hits cross-platform keywords (WSL2 / execution-context language). Operator intuition was that #8 (Windows/PowerShell port) is the heaviest piece. If you agree, manually swap #8 and #9 in PLAN.md before Phase 2 picks #8. The rubric is a heuristic; manual override is allowed.

iter 1: also added `.claude/` to `.gitignore` (the ralph-loop plugin writes session state there; should not be tracked).

iter 2 (2026-05-19): PR opened for #6 — added `-i`/`--repo-issues [name]` flag to clrepo, thin wrapper over `gh issue list` — https://github.com/freaxnx01/clrepo/pull/11. No test framework in repo, falling back to `bash -n` and manual smoke checks per Ralph prompt step 6 (documenting this once for future iterations). Also added `.nfs*` to `.gitignore` (NFS silly-rename temps appear briefly on the dev container's NFS mount).

iter 3 (2026-05-19): PR opened for #7 — added `--dashboard` cross-repo overview, parallel `gh issue list` fan-out — https://github.com/freaxnx01/clrepo/pull/12.

Versioning convention for parallel Ralph PRs: each PR bumps the next minor relative to the previous open Ralph PR's version, not main's. PR #11 (issue #6) claims 1.31.0, this PR (#12) claims 1.32.0. If PRs merge out of order, the later-merging PR has to resolve a version-line conflict by re-bumping. Open to revisit if conflicts get noisy.

iter 4 (2026-05-19): STOPPED without implementation. Re-read #4's body and noticed the author explicitly recommends "Best delivered on top of #3, so env-list and config-file-list arrive together with a single precedence story." The dependency markers I set earlier (#3 → #4) had the direction reversed — the correct order is #3 first (introduces config-file source), then #4 (extends to list semantics everywhere). Swapped the markers on the GitHub issues, reversed the deps in PLAN.md, and re-sorted. Next iteration should pick #3. No code committed in this iteration; only bookkeeping on main.

Lesson: when wiring dependency markers, also read the body for "Best delivered on top of #N" / "after #N lands" phrasing — these are the author's design intent and override gut judgments about which is foundational. Worth folding into the prompt's Phase 1 step 2 list of trigger phrases.

iter 5 (2026-05-19): PR opened for #3 — added `$_CLREPO_CONFIG/base` config-file fallback with env > file > default precedence — https://github.com/freaxnx01/clrepo/pull/13. Version bumped to 1.33.0 (reserving 1.31.0 for PR #11, 1.32.0 for PR #12).

iter 6 (2026-05-19): PR opened for #8 (the `epic`) — Windows/PowerShell support per docs/plans plan, all 7 tasks in one PR — https://github.com/freaxnx01/clrepo/pull/14. Version 1.34.0. First test file in the repo (tests/test_norm_path.sh, 11 assertions, all green). Expected merge conflict with PR #13 (same area modified — see PR description for resolution recipe). Plan execution went smoothly because the spec/plan docs were already in place; this is the pattern future epics should follow.
