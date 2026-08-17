# Glossary

Terms specific to bridge's local session layer.

Workflow vocabulary — enrichment run, quick-mode, one-way door, assumptions
block, agent-owned body, revise, run label, epic, milestone, label — lives in
[agent-workflow's glossary](https://github.com/freaxnx01/agent-workflow/blob/main/docs/glossary.md),
which is canonical for anything spanning repos. Only add a term here if it
describes bridge's own machinery.

## Attention state

Ephemeral, machine-local record of which session is blocked on human input.
Written by Claude Code hooks (`bridge-hooks/notify.sh` on `idle_prompt`,
debounced 120s, and on `elicitation_dialog` immediately; `clear-idle.sh` to
clear), stored under `~/.cache/bridge/sessions/`, read by `bridge sessions` and
the presence layer.

Lifetime is the box — it does not survive a reboot and is never persisted to a
forge. This is deliberate: attention state is about *now*, and a forge is for
what outlives the session.

## Slot

A numbered launch position for an agent session, with an associated repo,
worktree, and tmux session. Managed by `cmd/bridge/slots.go`; the unit
`bridge sessions` reports on, that `bridge sessions attach` targets, and that
hooks are parameterised by (`notify.sh` takes the slot number as `$1`).

## Presence

Whether the human is currently attached to a session, as opposed to whether the
session wants attention. The two are independent: a session can be waiting for
input while you are sitting in it. bridge-bot's paging gate consults presence so
Telegram pages only fire when you are *not* already looking at the pane.
