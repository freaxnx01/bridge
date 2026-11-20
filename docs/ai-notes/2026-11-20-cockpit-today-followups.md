# Cockpit Today — follow-ups (handoff 2026-11-20)

Source session: the "WebUI next level" session (worktree `webui-next-lvl`), which designed
Cockpit Today (`docs/design/cockpit-today/`) and filed the roadmap issues #379–#384.

## Done

- bridge #379: the dispatch journal (`internal/dispatch/journal.go`, `cmd/bridge/dispatch_journal.go`),
  merged as `3e6d0f3` via PR #398 and deployed.
- bridge #387: dispatch verdicts, gates and tick history on the pipeline board, merged via PR #399
  by another session. **This was the blocker for #380.**
- agent-workflow #593, #597 (ADR-023 `pre_agent` failure class), #601 (knowledge bundle): all merged.
- The knowledge bundle `docs/generated/software-factory-knowledge.md` was uploaded by hand to the
  "Software Factory" Claude Project on 2026-11-13. The app has no GitHub picker, so it needs to be
  re-uploaded by hand to refresh.
- agent-workflow #609 filed (`needs-enrichment`): drop the knowledge-bundle `push` trigger, because every
  regen moves `main` and puts open PRs BEHIND under strict protection.

## Open, in order

1. **bridge #380** `GET /api/digest`: still labelled `parked`, but its blocker (#387) is closed.
   - Check how #399 records ticks: does it extend `JournalRecord`/`ReadJournal` from #379, or did it
     add the separate `dispatch-ticks.jsonl` its spec proposed?
   - Then unpark #380, align its spec to that, enrich, and dispatch.
2. bridge #381–#384 (`needs-enrichment`), all built on top of #380:
   - #381 digest in the Inbox
   - #382 merge from the cockpit behind a head-SHA guard
   - #383 read-only PR diff
   - #384 quota line
3. agent-workflow #609: enrich and dispatch (small `ci` change).
4. Not captured yet: #601's docs say the Project syncs the bundle through the GitHub picker. In fact
   it is a manual re-upload. Add this to #609 or open a docs issue.

## Rules carried over from the user

- GitHub writes (issues, labels, comments, PRs) only after an explicit OK, each time. Use
  `direnv exec /home/admin/repos/github/freaxnx01 …`.
- Ask decisions with AskUserQuestion, with the recommendation marked.
- Report at the end of each step: done / next / waiting on.
