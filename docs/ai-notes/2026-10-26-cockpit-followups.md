# Cockpit slice 1 — deferred follow-ups

From per-task and final reviews (2026-10-26). None block merge. Also parked: `bridge dispatch status` shows last tick 2026-07-29 and budget 3870% (stale or broken), and ETag/rate-limit backoff for collectors.

- Task 2: minor (deferred): planHeadingRE lacks doc comment; matches headings inside code fences (heuristic per brief)
- Task 2: minor (deferred): no negative tests for h1 / trailing-text plan headings; reason precedence combos untested
- Task 3: minor (deferred): no test for Held/NeedsEnrichment Created-ascending order; run sort with equal Created not exercised
- Task 3: minor (deferred): repoForCWD HasPrefix(p+"/") wrong if Path is "/" (unrealistic); Labels slice aliased into cards
- Task 4: minor (deferred): stale overwrite — in-flight CollectIssues can overwrite a newer CollectRepo result (window ≈ one fetch)
- Task 4: minor (deferred): first-fetch failure stores baseRepoData(r,false) → wired repo shown unwired (collector.go:198-204, :320)
- Task 4: minor (deferred): errs for repos removed from Repos() never pruned
- Task 4: minor (deferred): cancel mid-collect stamps collectedAt + records context-canceled errors; Paused() called under mutex
- Task 4: minor (deferred): PRs/Milestones failure reported as "issues o/r"; tests not table-driven; Run-cancel test allows 5s vs brief's 1s; fan-out test doesn't catch serialization
- Task 5: minor (deferred): launch_test.go discards ReadFile error (`log, _ :=`) following file's existing style
- Task 5: minor (deferred): origin allowlist case-sensitive (BRIDGE_COCKPIT_ORIGINS mixed-case entry never matches)
- Task 6: minor (deferred): Running shows partial list w/o hint while sessions uncollected; extra paused note in Pipeline (brief-sanctioned)
- Task 6: minor (deferred): needs-enrichment cards show no age — needs created/updated field in IssueCard (backend follow-up)
- Task 6: minor (deferred): pipeline card line 2 shows issue title not run name; no phone ☰ (nav wraps); desktop aria-expanded on collapse toggle wrong; action re-click after launched; error strip shows relative not clock time
- Task 6: minor (deferred): test gaps — empty state, stale dimming, non-session_ RC negative, no RunningCard/IssueCard unit tests
- Task 6: minor (deferred): two pipeline cards with same url would collide keys (run URLs unique in practice); .refresh-error CSS placement; weak refresh-error test assertion
- Task 7: minor (deferred): cockpit.md points to dispatch.md for the plan rule, but dispatch.md doesn't mention it; 405 row only reachable on GET route
