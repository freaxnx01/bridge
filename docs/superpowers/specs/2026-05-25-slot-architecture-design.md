# Slot Architecture Redesign

**Date:** 2026-05-25
**Issue:** [#32 — Slots exhausted: improve displacement UX when all slots are busy](https://github.com/freaxnx01/clrepo/issues/32)
**Status:** Approved, pending implementation

## Context

The original slot system was designed around a 1-bot-per-slot Telegram model: each of the 6 slots had a dedicated Telegram bot whose token was stored in `slot-tokens.json` via Passbolt. The slot count (`CLREPO_MAX_SLOTS=6`) was implicitly capped by the number of provisioned bots.

The workflow has since shifted:
- Claude Remote Control (`--remote-control`) is the primary session-steering mechanism
- `clrepo-bot` (standalone Python bot) is the only Telegram interface in active use
- Per-slot bots are no longer used or needed

The 6-slot cap now creates false scarcity: slots exhaust, triggering auto-displacement of the oldest session with no user input.

## Design: Approach A — Minimal surgery

### 1. Slot allocator — remove cap and displacement

`_clrepo_slot_allocate` currently loops `seq 1 $_CLREPO_MAX_SLOTS` and displaces the oldest slot if all are busy.

**Change:** Remove the upper bound entirely. The allocator scans `slots.json` for the lowest integer key ≥ 1 that is absent or `null`. No ceiling, no displacement branch.

- `_CLREPO_MAX_SLOTS` variable and all references removed
- The `# All busy — displace oldest` block (including `sleep 5` countdown) removed
- The per-slot-token lookup (`pb_id` / `passbolt` call) in `_clrepo_slot_allocate` removed
- The "no bot token for slot N" warning removed
- Dead-slot reconciliation (PID/tmux liveness check) at the top of `_clrepo_slot_allocate` stays unchanged — it frees stale slots so their numbers are reused naturally

`~/.claude-s<N>/` dirs accumulate monotonically but carry no functional cost.

### 2. Notification layer — single `_clrepo_notify` helper

Replace all per-slot Telegram functions with one helper:

```
_clrepo_notify <text>
```

**Implementation:**
1. Read `~/.cache/clrepo/clrepo-bot.json` → `passbolt_resource_id` and `telegram_owner_id`
2. Resolve bot token: `passbolt get resource --id <id>`
3. POST to `https://api.telegram.org/bot<token>/sendMessage`
4. Best-effort — any failure returns 0 silently

**Call site mapping:**

| Removed function | Replaced by |
|---|---|
| `_clrepo_telegram_setup` | `_clrepo_notify` with session-start message |
| `_clrepo_telegram_cleanup` | `_clrepo_notify` with session-end message |
| `_clrepo_telegram_page` | `_clrepo_notify` (idle / usage-limit pages) |
| `_clrepo_admin_status_update` | **Dropped** — aggregate status via `clrepo --status` and clrepo-bot `/status` |

### 3. Removed components

**From `clrepo.sh`:**
- `_CLREPO_MAX_SLOTS` and all references
- `_CLREPO_SLOT_TOKENS` and all references
- `_clrepo_telegram_setup`
- `_clrepo_telegram_cleanup`
- `_clrepo_telegram_page`
- `_clrepo_admin_status_update`
- Displacement block in `_clrepo_slot_allocate`
- Per-slot-token lookup block in `_clrepo_slot_allocate`
- Per-slot bot-name wiring in `_clrepo_install_hooks` (hook file install itself stays)

**From `setup-claude-channels.sh`:**
- Section 2 entirely (per-slot bot token loop `for n in $(seq 0 "$MAX")`)
- `TOKENS` / `slot-tokens.json` writes
- `MAX` variable

**Data:**
- `~/.cache/clrepo/slot-tokens.json` — no longer written or read; existing file on disk ignored

**`clrepo-bot/`:** no changes required.

### 4. Hook and watcher integration

**`clrepo-hooks/notify.sh`:** Currently passes slot number to select a per-slot bot token. After: calls `_clrepo_notify` directly with the notification text. Slot number kept in message body for context only.

**`clrepo-watcher.sh`:** Replaces `_clrepo_telegram_page` call with `_clrepo_notify`. Removes `slot-tokens.json` lookup.

**`setup-claude-channels.sh` post-change shape:**
1. Telegram owner (user_id) — unchanged
2. ~~Per-slot bot tokens~~ — removed
3. clrepo-bot Passbolt resource ID — unchanged, now the only token

**`_clrepo_slot_creds_check`:** Unaffected — checks Remote Control credentials only.

## What does NOT change

- `~/.claude-s<N>/` config dir scheme and CLAUDE_CONFIG_DIR export
- Slot numbering identity (hooks, `--status`, `clrepo-bot` all reference slot number)
- `clrepo-bot` Python codebase
- Dead-slot reconciliation logic
- `--slot N` forced-slot flag
- `--free N` manual slot release
- `--no-channel` legacy mode
- `clrepo --status` output

## Success criteria

- `clrepo <repo>` never blocks or prompts when slots are exhausted — it always allocates the next free number
- All lifecycle Telegram notifications (start, idle, usage limit, end) arrive via clrepo-bot
- `setup-claude-channels.sh` no longer asks about per-slot bots
- `slot-tokens.json` is neither read nor written
- `_CLREPO_MAX_SLOTS` is gone from the codebase
