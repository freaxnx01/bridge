# Agent View vs. bridge — analysis (starter)

_Started 2026-06-02. Tracks #131. Status: **draft / open questions** — fill in as the spike progresses._

> **Prior art:** [`docs/positioning.md`](../positioning.md) (2026-05-21, closed #21) already
> concluded bridge and the Claude Agents view are **complementary, not overlapping**. This note
> does **not** restart that debate — it revisits the verdict in light of the published
> [Agent View docs](https://code.claude.com/docs/en/agent-view) and the specific tension that
> Agent View and Remote Control **cannot currently be combined**.

## The tension (new framing for #131)

Two Claude capabilities solve overlapping-but-distinct problems, and today they can't be combined:

| Capability | What it is | Reach |
|---|---|---|
| **Agent View** (`claude agents`) | Local supervisor managing background sessions | Local-only |
| **Remote Control** (`claude --rc`) | Tunnels a *single interactive* session to claude.ai/code or mobile | Remote |

bridge sits between them: it launches tmux-wrapped sessions (slot tracking + per-slot Telegram
pages) and opts sessions into Remote Control at launch. Agent View overlaps the "manage many
background sessions" half of that.

## Open questions

1. **Overlap vs. obsolescence** — does the published Agent View change the #21 verdict? Which
   bridge responsibilities does it now cover natively (background management, persistence,
   dashboard) and which stay uniquely bridge's (forge-aware discovery, Telegram/RC integration,
   tmux slot model, non-Claude launchers)?
2. **`claude --bg` instead of tmux** — could the Claude supervisor own background persistence,
   with `claude agents` as the dashboard, replacing the tmux slot model for background tasks?
   What would bridge lose (non-Claude launchers, slot registry, Telegram paging)?
3. **`claude agents --json` → locutus** — most promising near-term integration. Pipe the JSON
   snapshot into the Telegram bot for a read-only remote status report without attaching to the
   terminal. Verify the JSON shape and what fields are exposed.
4. **Combining the two models** — is there an architecture where background supervision (Agent
   View) and remote interactivity (Remote Control) coexist, with bridge as the glue?

## What to verify (facts to nail down before recommending)

- [ ] Exact `claude agents` / `claude --bg` CLI surface and flags (from the docs + a local run).
- [ ] Shape of `claude agents --json` output — fields, stability, session identifiers.
- [ ] Whether Agent View can observe or launch Remote-Control-enabled sessions (the "can't
      combine" claim — confirm it still holds and under what conditions).
- [ ] Whether `claude --bg` sessions survive across reboots / shell exits like tmux slots do.
- [ ] Cross-platform story (bridge's tmux model is Unix-only; what about Agent View?).

## Working hypothesis (to confirm or refute)

Following #21: bridge stays the **local-repo hub + launch decision point**; Agent View is the
**session manager**. The new wrinkle is reporting — `claude agents --json` piped to locutus could
give *pull-style* remote status that complements bridge's existing *push* Telegram notifications,
without bridge re-implementing session transcripts or steering.

## Deliverable

Promote this note to an ADR (or update `docs/positioning.md`) once the questions above are
answered, with a recommendation: deprecate overlapping parts, integrate Agent View, or hold.
