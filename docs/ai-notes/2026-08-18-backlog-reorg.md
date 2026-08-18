# Backlog reorg — WebUI v1 + a working dispatcher

Session brief. Status: **paused after step 2.** Steps 1 and 2 are done and
merged; **step 3 (`/triage`) is where the next session starts.**
Session 1 ran 2026-08-18; this file was last updated at its close.

This file is the session-scoped half of the advisor prompt. The durable half —
role, ground rules, known failure modes, tool notes — is
[`agent-workflow/docs/ADVISOR-PROMPT.md`](https://github.com/freaxnx01/agent-workflow/blob/main/docs/ADVISOR-PROMPT.md)
and that file wins on any conflict. Repo state is
[`FACTORY-MAP.md`](https://github.com/freaxnx01/agent-workflow/blob/main/docs/FACTORY-MAP.md).

The Claude Project instruction should be a pointer, not a copy:

```text
Read docs/ADVISOR-PROMPT.md and docs/FACTORY-MAP.md in freaxnx01/agent-workflow,
then follow it. Today: read freaxnx01/bridge
docs/ai-notes/2026-08-18-backlog-reorg.md and work it.
```

## Goal

Reorganise the bridge backlog toward **a first WebUI version** and **a working
dispatcher**.

## Scope boundary

In scope: the six steps below. Everything else found along the way gets written
down, not acted on.

## Verified state (2026-08-17/18) — do not re-derive

- **54 open issues** in bridge. None has a milestone. None has a `size:` label.
- `typeRank` (`internal/dispatch/order.go`) matches `feat`; the repo labels use
  `enhancement`. Rungs 2, 3 and 4 of the five-rung ladder are therefore all
  inert — dispatch degrades to bugs-first, then oldest-first. Filed as **#240**.
- **14 issues carry `needs-enrichment`**, which bars them from every automated
  route (`/route` Step 2) and from dispatch (`Eligible`, first check).
- **#211 is four tiers; only tier 1 shipped.** Tier 2 (`list_milestones`,
  `list_prs`, cross-repo `search_issues`) is unbuilt and blocks
  agent-workflow#149. Do **not** file a new milestone tool — it is #211 tier 2.
  Do **not** close #211.
- Decisions resolved and written into bodies: **#180** (1–3 resolved, 4 deferred
  to agent-workflow#252), **#179** (decision 5 = WebUI), **#242** (surface =
  WebUI).
- **WebUI is the operating surface for the whole factory.** The TUI/nav board is
  sunset — no new rendering work targets it.

### Added this session (code-verified 2026-08-18)

- **Issues are already served** by the REST layer: `GET
  /api/repos/{owner}/{name}` returns `RepoDetail{Repo, Sessions, Issues}` —
  `internal/api/repos.go:22`. No frontend consumer yet.
- **Milestones are not served at all.** `ListOpenMilestones` exists only on
  `GithubClient` (`internal/forge/github.go:929`), is *not* on the
  `forge.Client` interface, and has exactly one caller —
  `cmd/bridge/dispatch.go:201`. `internal/forge/forgejo.go` has zero milestone
  code. Cross-forge milestone rendering has no data source today.
- **A WebUI test pattern already exists but is unenforced.** `web/src/App.test.js`
  and `web/src/lib/stores/sse.test.js` use Vitest + jsdom +
  `@testing-library/svelte`, mocking stores at the module boundary via
  `vi.mock`. There is no `just web-test` recipe and no CI workflow runs
  `npm test`. This corrects #242's "test approach not yet established".

## Plan

1. **Enrich #215** — foundation for the WebUI milestone; establishes the WebUI
   test-approach pattern that #242 and #179 reuse. ✅ **done 2026-08-18**
   (read-only · GitHub-only · milestone→issues per repo; `needs-enrichment`
   cleared, verified by read-back).
2. **Patch typeRank (#240)** — accept `feat`, `feature`, `enhancement` at one
   rank. Table-driven test. Do **not** relabel the backlog.
   ✅ **done 2026-08-18** — PR **#248**. TDD, red first; the failing run showed
   `bug`, `fix`, `feat` and case-insensitivity already worked, so the fix stayed
   to one loop. All gates green locally (`gofmt`, `vet`, `golangci-lint` on the
   package, `go test -race ./...` 24 packages). **29 issues moved up** the
   dispatch order — rung 3 is live.
3. **`/triage`** — ⬅️ **resume here.** Read the backlog *with bodies*. Authoritative; every
   title-based grouping from the previous session was provisional and four
   turned out wrong.
4. **Park what does not serve WebUI v1 or the dispatcher.** One at a time, each
   with a `🧊 parked: <reason>` comment — the label alone leaves `/parked list`
   showing `—`. `roadmap` = unscheduled-but-intended; `🧊 parked` = stopped.
5. **Create the milestone(s) WITH `due_on`.** An undated milestone leaves
   `compareDue` reading a zero time and rung 2 stays inert. agent-workflow#149
   documents one-active-milestone-per-repo, so two parallel active milestones
   in bridge would break `--milestone current`.
6. **`/milestone triage`** to assign, then `size:` labels on milestoned issues
   only.

## Do NOT

- Do **not** park #170 or #171 — spike-gated on agent-workflow#252.
- Do **not** park #204 (Forgejo client falls back to codeberg.org and leaks the
  token). It serves neither milestone but it is a credential disclosure open
  since 20 July. **Fix it.**
- Do **not** close #211. Tier 1 only is done.
- Do **not** bulk-anything. Both `/parked review` and `/milestone triage` are
  explicitly one-at-a-time.

## Acceptance test for the whole reorg

Capture `bridge dispatch` output **before** step 2 and again after step 6. If
the order is unchanged, the reorg failed.

Baseline captured 2026-08-18 from worktree HEAD `31864e2`: `1 dispatched, 110
skipped`, bridge sorting first and purely bugs-then-oldest within it — `#114,
#144, #154, #156, #204`.

## Landed in session 1

| PR / issue | What | State |
|---|---|---|
| **#248** | typeRank accepts `feat`/`feature`/`enhancement`; `Closes #240` | merged |
| **#249** | this file | merged |
| **#250** | `repo_test.go` SA5011 lint fix | merged — `a105fbf` |
| **#251** | pin the Go patch so a toolchain bump cannot change lint results | filed, unenriched |

### The detour that cost most of the session

Patching typeRank turned every PR red on `golangci-lint`, in a file neither PR
touched:

```text
internal/core/repo_test.go:122:5: SA5011(related information): this check suggests that the pointer can be nil
internal/core/repo_test.go:125:11: SA5011: possible nil pointer dereference (staticcheck)
```

It was **pre-existing on `main`**, not a regression — proven by a docs-only PR
with zero Go files failing with the identical two errors. `main`'s own run on
`31864e2` had passed at 19:07; both PRs failed at 19:49 against that same base
with the same pinned `version: v2.1.6`.

Mechanism, still **inferred rather than proven**: `.github/workflows/go.yml` uses
`install-mode: goinstall`, which rebuilds golangci-lint from source with the
runner's Go, while `go-version: '1.25'` floats the patch. A toolchain patch bump
changes staticcheck's behaviour underneath a pinned linter version. Confirming it
means re-running `main`'s `go.yml` against an older toolchain. Tracked in #251.

Fixed in #250 by tracking the match **by index instead of by pointer**, so there
is no pointer left to be nil regardless of what staticcheck concludes about
`t.Fatalf`. Note that the failure was **not locally reproducible** — local
`golangci-lint v2.1.6` reported `0 issues` before and after. CI was the only
verification.

## Gotchas found this session

Candidates for promotion into `ADVISOR-PROMPT.md`'s Tool notes:

- **`bridge dispatch` returns `[]` and exits `0` with no `GH_TOKEN`.** The
  warning only appears at `-vv` (`dispatch: no GitHub client available`). A
  baseline captured without a token is silently empty and the before/after
  comparison becomes vacuous. Export `GH_TOKEN=$(gh auth token)` first.
- **A dated milestone makes every un-milestoned issue in that repo ineligible.**
  `internal/dispatch/eligible.go:105` — `activeMilestone != "" && i.Milestone !=
  activeMilestone` → skipped as "outside active milestone". Step 5 therefore
  strands #204 unless it is in the milestone or fixed first.
- **CI can go red on `main` with nothing pushed.** The golangci-lint version is
  pinned but the Go toolchain that builds it is not — see the detour above and
  #251. A red PR is not automatically the PR's fault; check whether a docs-only
  branch fails the same way before debugging your own diff.
- **`ActiveMilestone` picks the earliest due date and treats an undated
  milestone as inactive** (`eligible.go:46`). Two dated milestones do not error
  — the later one silently goes inert. This is the mechanism behind
  agent-workflow#149's one-active-milestone rule.

## Discoveries parked — written down, not acted on

Per the scope rule: found while working step 2, none of them in scope.

- **`main`'s branch protection does not match `CLAUDE.md`.**
  `gh api repos/freaxnx01/bridge/branches/main/protection` returns
  `{"checks":["golangci-lint","govulncheck"],"reviews":null,"enforce_admins":false,"strict":false}`.
  So the documented "at least 1 PR review" is **not enforced**, and **`test` is
  not a required check** — a PR with a failing suite can merge as long as lint
  and vuln pass. Not filed yet.
- **`bridge dispatch` truncates issue titles mid-rune**, so its own `-vv` output
  is not valid UTF-8 (see the replacement characters in the appendix below).
  Cosmetic, but it means the output cannot be piped into anything that assumes
  UTF-8 without a decode-with-replacement step. Not filed yet.
- **At least one issue's labels disagree with its title convention** —
  `bridge #131 chore(core): analyze whether…` rose with the feature block after
  the typeRank patch, so it carries `enhancement` despite a `chore` title. A
  backlog-wide label/title audit is *not* part of this reorg (step 2 explicitly
  says do not relabel), but it is worth knowing the two signals diverge.

## Next session starts here

Prompt:

```text
Read docs/ADVISOR-PROMPT.md and docs/FACTORY-MAP.md in freaxnx01/agent-workflow,
then follow it. Today: read freaxnx01/bridge
docs/ai-notes/2026-08-18-backlog-reorg.md and continue the reorg from step 3.
```

First actions, in order:

1. **`/triage`** — read the backlog *with bodies*. Every title-based grouping
   from the pre-session pass was provisional and four turned out wrong.
2. Then steps 4, 5 and 6 as written in the Plan above, one issue at a time.
3. Before step 5 creates a dated milestone, settle **#204** — a dated milestone
   makes every un-milestoned issue in the repo ineligible, and #204 is a
   credential disclosure that must not be stranded.

Re-capture the dispatch order after step 6 and diff it against the appendix. If
the order is unchanged, the reorg failed.

## Appendix — dispatch baseline, verbatim

Captured 2026-08-18 from worktree HEAD `31864e2`, **before** the typeRank patch,
via `GH_TOKEN=$(gh auth token) bridge dispatch --dry-run -vv`. This is the
artefact the step-6 acceptance test diffs against; it is recorded here because
the original capture lived in a session-scoped temp directory that does not
survive the session.

To compare after step 6, strip the decision suffix from both sides so only the
ordering is compared:

```bash
diff <(sed 's/ → .*//' baseline.txt) <(sed 's/ → .*//' after.txt)
```

> The `�` characters below are faithful: `bridge dispatch` truncates issue
> titles mid-rune, so its own output is not valid UTF-8. Noted as a discovery,
> not fixed here.

```text
  bridge       #114  fix(nav): nested-tmux launc… → dispatch
  bridge       #144  fix(core): CLI repo address… → SKIP (global cap 5/5)
  bridge       #154  fix(pwsh): tmux/CC CLI sess… → SKIP (global cap 5/5)
  bridge       #156  fix(pwsh): tmux detach (Ctr… → SKIP (global cap 5/5)
  bridge       #204  fix(forge): Forgejo client … → SKIP (global cap 5/5)
  ai-instructions #28   fix(browser-game): fold i18… → SKIP (global cap 5/5)
  StringKing   #1    PCL -> .NET Standard         → SKIP (global cap 5/5)
  StringKing   #2    new candidate: XML RemoveAl… → SKIP (global cap 5/5)
  StringKing   #3    new candidate: Lorem ipsum … → SKIP (global cap 5/5)
  StringKing   #4    todo                         → SKIP (global cap 5/5)
  StringKing   #5    PowerShell module            → SKIP (global cap 5/5)
  StringKing   #6    StringKing N++ Plugin        → SKIP (global cap 5/5)
  StringKing   #7    StringKing Electron          → SKIP (global cap 5/5)
  StringKing   #8    StringKing PlayStore?        → SKIP (global cap 5/5)
  StringKing   #9    nuget                        → SKIP (global cap 5/5)
  StringKing   #10   bash-like functions          → SKIP (global cap 5/5)
  StringKing   #11   Ideen String-Funktionen      → SKIP (global cap 5/5)
  StringKing   #12   StringFunctionsDnpExtensions → SKIP (global cap 5/5)
  StringKing   #13   Umständlicher Methoden-Auf…  → SKIP (global cap 5/5)
  StringKing   #14   function aufzählung          → SKIP (global cap 5/5)
  StringKing   #15   lorem ipsum                  → SKIP (global cap 5/5)
  StringKing   #17   Nuget, StringKing -> Extens… → SKIP (global cap 5/5)
  StringKing   #20   Have a look at https://gitl… → SKIP (global cap 5/5)
  StringKing   #21   UI like WingetUI             → SKIP (global cap 5/5)
  agent-skills #2    feat(propose-ai-instruction… → SKIP (global cap 5/5)
  agent-skills #3    feat(propose-ai-instruction… → SKIP (global cap 5/5)
  agent-skills #4    feat(propose-ai-instruction… → SKIP (global cap 5/5)
  agent-workflow #153  chore(commands): analyze wh… → SKIP (global cap 5/5)
  bridge       #69   test(completion): manual pw… → SKIP (global cap 5/5)
  bridge       #74   test(shim): Pester tests fo… → SKIP (global cap 5/5)
  bridge       #75   test(launcher): integration… → SKIP (global cap 5/5)
  bridge       #87   feat(focus): focus-repo lis… → SKIP (global cap 5/5)
  bridge       #89   feat(rm): delete remote for… → SKIP (global cap 5/5)
  bridge       #91   feat(sync): session-close a… → SKIP (global cap 5/5)
  bridge       #93   feat(doctor): forge-target … → SKIP (global cap 5/5)
  bridge       #94   feat: worktree status overv… → SKIP (global cap 5/5)
  bridge       #95   feat(issues): single-repo i… → SKIP (global cap 5/5)
  bridge       #96   feat(tui): restore cross-re… → SKIP (global cap 5/5)
  bridge       #97   feat: RC-URL status picker … → SKIP (global cap 5/5)
  bridge       #98   feat: legacy flag-spelling … → SKIP (global cap 5/5)
  bridge       #131  chore(core): analyze whethe… → SKIP (global cap 5/5)
  quotes       #2    [Claude Code / claude-opus-… → SKIP (global cap 5/5)
  quotes       #3    [GH Copilot] Deploy quotes … → SKIP (global cap 5/5)
  agent-workflow #91   fix(dotnet-quality): method… → SKIP (global cap 5/5)
  bridge       #160  feat: delete repos (remote … → SKIP (global cap 5/5)
  agent-workflow #145  feat(hooks): add Windows/Po… → SKIP (global cap 5/5)
  bridge       #162  fix(config): wire GitLab AP… → SKIP (global cap 5/5)
  bridge       #163  feat(bot): /ask command —…   → SKIP (global cap 5/5)
  bridge       #164  feat(bot): /status — enri…   → SKIP (global cap 5/5)
  bridge       #165  feat(bot): session summary … → SKIP (global cap 5/5)
  bridge       #166  feat(bot): /plan — AI-exp…   → SKIP (global cap 5/5)
  bridge       #167  feat(bot): auto-label issue… → SKIP (global cap 5/5)
  bridge       #168  feat(webui): 'What next?' �… → SKIP (global cap 5/5)
  bridge       #169  feat(webui): stale issue de… → SKIP (global cap 5/5)
  bridge       #170  feat(nav/webui): bridge age… → SKIP (global cap 5/5)
  bridge       #171  feat(webui): wire agent pin… → SKIP (global cap 5/5)
  quotes       #12   [OpenCode / codestral] feat… → SKIP (global cap 5/5)
  FlowHub-CAS-AISE #159  G1 (LLM02): outbound redact… → SKIP (global cap 5/5)
  FlowHub-CAS-AISE #160  G2 (LLM08): scope capture r… → SKIP (global cap 5/5)
  FlowHub-CAS-AISE #161  G3 (LLM10): document the Op… → SKIP (global cap 5/5)
  FlowHub-CAS-AISE #162  G4 (ASI03): review per-skil… → SKIP (global cap 5/5)
  agent-workflow #146  feat(commands): add /goal v… → SKIP (global cap 5/5)
  agent-workflow #111  feat(models): adopt Claude … → SKIP (global cap 5/5)
  agent-workflow #114  chore(models): benchmark qw… → SKIP (global cap 5/5)
  agent-workflow #147  docs(commands): clarify cro… → SKIP (global cap 5/5)
  agent-workflow #148  chore(commands): analyze wh… → SKIP (global cap 5/5)
  agent-workflow #150  chore(commands): decide the… → SKIP (global cap 5/5)
  agent-workflow #151  chore(commands): manage sta… → SKIP (global cap 5/5)
  agent-workflow #152  chore(commands): analyze in… → SKIP (global cap 5/5)
  game-barrel-shooter #2    vid                          → SKIP (global cap 5/5)
  game-tschau-sepp #1    CPU vs. CPU spectator mode   → SKIP (global cap 5/5)
  game-tschau-sepp #2    Local multiplayer — two h…   → SKIP (global cap 5/5)
  game-moki-racer #1    Tag-/Nacht-Modus             → SKIP (global cap 5/5)
  game-moki-racer #2    Nacht: Fackeln am Ufer und … → SKIP (global cap 5/5)
  SaveOutlookCalendar #1    Replace Google Apps Script … → SKIP (global cap 5/5)
  flowhub      #1    feat(enrichment): capture e… → SKIP (global cap 5/5)
  flowhub      #2    feat(enrichment): add web-s… → SKIP (global cap 5/5)
  flowhub      #3    feat(providers): add additi… → SKIP (global cap 5/5)
  flowhub      #4    feat(integrations): paperle… → SKIP (global cap 5/5)
  flowhub      #14   feat(bridge): alias capture… → SKIP (global cap 5/5)
  agent-workflow #143  fix(linker): close mid-run … → SKIP (global cap 5/5)
  agent-workflow #144  fix(bootstrap): make agent-… → SKIP (global cap 5/5)
  bridge       #207  refactor(mcp): replace errg… → SKIP (global cap 5/5)
  agent-workflow #156  feat(gh): support DueDate/D… → SKIP (global cap 5/5)
  agent-workflow #157  feat(commands): add lightwe… → SKIP (global cap 5/5)
  agent-workflow #158  feat(commands): add skill/s… → SKIP (global cap 5/5)
  agent-action-sandbox #7    test: bridge MCP create_iss… → SKIP (global cap 5/5)
  bridge       #211  feat(mcp): mutating + lifec… → SKIP (global cap 5/5)
  game-cluck-and-load #1    Add MP P2P Co-op mode        → SKIP (global cap 5/5)
  bridge       #212  test(mcp): add per-tool JSO… → SKIP (global cap 5/5)
  bridge       #213  fix(mcp): tier-1 audit-log … → SKIP (global cap 5/5)
  agent-workflow #161  fix(ci): max_turns=30 is ha… → SKIP (global cap 5/5)
  agent-workflow #162  feat(gh-implement): add a s… → SKIP (global cap 5/5)
  game-tschau-sepp #7    Let player choose number of… → SKIP (global cap 5/5)
  ideas-lab    #1    Game idea: Battlechess clon… → SKIP (global cap 5/5)
  ideas-lab    #2    Game idea: Clawd (Claude Co… → SKIP (global cap 5/5)
  ideas-lab    #3    Game idea: mountain airbase… → SKIP (global cap 5/5)
  bridge       #217  feat(mcp): support attachme… → SKIP (global cap 5/5)
  bridge       #218  feat(core): automate onboar… → SKIP (global cap 5/5)
  agent-workflow #205  docs: purge stale claude-pi… → SKIP (global cap 5/5)
  agent-workflow #206  feat: warn when consumer tr… → SKIP (global cap 5/5)
  agent-workflow #207  chore: v2 breaking renames … → SKIP (global cap 5/5)
  game-plod    #2    i18n: add DE/EN support (de… → SKIP (global cap 5/5)
  agent-workflow #224  fix(classify-task): heurist… → SKIP (global cap 5/5)
  agent-workflow #226  fix(opencode): agent pushes… → SKIP (global cap 5/5)
  agent-workflow #227  fix(opencode): agent occasi… → SKIP (global cap 5/5)
  bridge       #232  fix(mcp): mcp-remote hangs … → SKIP (global cap 5/5)
  bridge       #234  chore(dispatch): prioritize… → SKIP (global cap 5/5)
  agent-workflow #252  spike(enrich): is a quick-m… → SKIP (global cap 5/5)
  bridge       #240  fix(dispatch): typeRank mat… → SKIP (global cap 5/5)
  bridge       #242  feat(webui): render milesto… → SKIP (global cap 5/5)

1 dispatched, 110 skipped
```
