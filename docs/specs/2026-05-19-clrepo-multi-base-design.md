# clrepo multi-base support — Design

- **Issue:** #4
- **Date:** 2026-05-19
- **Status:** Draft

## 1. Goal & non-goals

**Goal.** Let a user keep repos in more than one tree (e.g. `~/projects/repos` for personal + `~/work/repos` for work) and see them all through one `clrepo` invocation. Discovery, status, the picker, and CWD-relative launch should all span every configured base.

**Non-goals.**

- Cross-base merging at the forge level: each base keeps its own `github/<owner>/(public|private)`, `gitlab/<owner>`, `git-forgejo`, `ado` subtree. No attempt to deduplicate the same repo cloned under two bases.
- Re-keying slot/Telegram/RC state. Today these key off absolute paths; that stays stable across this change, so no migration is needed.
- Cache key changes for `~/.cache/clrepo/repo-meta.json` etc. Caches are scoped per base implicitly via path.
- The `--base <dir>` per-invocation override — that's #5 and depends on this.

## 2. Architecture

`_CLREPO_BASE` becomes the *first element* of a new array `_CLREPO_BASES`. Existing code that reads `_CLREPO_BASE` continues to work (single-base UX unchanged). Multi-base–aware code reads `_CLREPO_BASES` and iterates.

```
CLREPO_BASE env (":"-separated)
  │
  ▼
$_CLREPO_CONFIG/base file (one path per line — extends #3)
  │
  ▼  parse + ~/$HOME expand + dedupe + drop missing
  ▼
_CLREPO_BASES=( "/home/me/projects/repos" "/home/me/work/repos" )
_CLREPO_BASE="${_CLREPO_BASES[0]}"   # backward-compat alias
```

Precedence (same as #3, extended to list semantics):

1. `CLREPO_BASE` env var — split on `:`. Empty string elements ignored.
2. `$_CLREPO_CONFIG/base` file — every non-empty, non-`#` line is one base.
3. Default — `["$HOME/projects/repos"]`.

Whichever source wins, it wins as a whole list. The sources are not merged. (This matches the "single precedence story" the issue body asks for.)

## 3. Call-site adaptation

Three categories of call sites:

**A. Discovery / enumeration — iterate every base.** `_clrepo_targets`, the `find` calls at clrepo.sh:1579, the `all=$(find …)` line in `clrepo()`. Each emits paths *prefixed by their base index* so the consumer can resolve back to the originating base. Two emission shapes considered:

- **Option A1 (chosen):** keep the existing relative-path output (`github/foo/public/bar`) and store the originating absolute base in a parallel structure (`_CLREPO_LAST_TARGET_BASE` or similar) or in a TSV column. Used for the picker and for the cwd-resolution lookup.
- **Option A2 (rejected):** prefix every rel with `<base_index>:<rel>`. Forces every consumer to re-split; messier.

Concretely: `_clrepo_targets` gains a 5th TSV column `<base_abs>`. `find` callers in `clrepo()` iterate the bases.

**B. Path resolution (CWD → rel) — find the owning base.** The line `${git_root#$_CLREPO_BASE/}` (clrepo.sh:2683 area) becomes a loop: for each base in `_CLREPO_BASES`, check if `$git_root` starts with `$base/`; if so, the rel is `${git_root#$base/}` and the owning base is `$base`. First match wins (matches the precedence order).

**C. Launch / cd / clone — use the owning base.** `cd "$_CLREPO_BASE/$rel"` becomes `cd "$owning_base/$rel"`. For paths coming out of category (A) the owning base is known. For name-based lookup (basename matcher), we run the basename match against the union of all bases and remember which base each hit came from.

## 4. Display

Single-base setups must look exactly like today — no new columns, no prefixes.

Multi-base setups (`${#_CLREPO_BASES[@]} > 1`) add a *base label* to user-facing listings:

- **Auto-derived label:** the basename of each base, with collision-detection. `~/projects/repos` → `repos`, `~/work/repos` → `repos` (collision!) → fall back to `<parent>/<basename>`: `projects/repos` vs `work/repos`. If still ambiguous: numeric index `b1`, `b2`.
- **Where it appears:** `--status` row prefix, `--pick` picker row, the main fzf picker row when `_clrepo_remote_list` is involved. NOT in error messages (those say "across bases X, Y, Z").
- **Visual form:** `<label>:<rel>` (e.g. `work:github/megacorp/private/api`). Indent in fzf so it sorts cleanly.

"No targets discovered" messages (clrepo.sh:1518/1646/1706/2683) now say "under any of: `<base1>`, `<base2>`, …".

## 5. Backward compatibility

- Existing single-base setups: zero behavioural change. `CLREPO_BASE=/home/me/repos` still works; `find` walks one tree; output has no labels.
- Existing config-file (#3) single-line file: works as before, treated as a list of one.
- Slot/Telegram/RC state keyed by absolute repo path: unaffected.
- All scripts in `clrepo-admin-commands/`, `clrepo-watcher.sh`, etc. — they read `_CLREPO_BASE` directly. They continue to work as if single-base. They MAY be later updated to iterate `_CLREPO_BASES`, but that's a follow-up.

## 6. Edge cases

- **Empty `CLREPO_BASE` env (`CLREPO_BASE=`):** treated as unset → fall through to config file.
- **`:` in a base path itself:** undocumented today, and `:` is illegal in Linux dir names anyway. Ignored.
- **Trailing `/` in a base:** normalized off at parse time so prefix matching stays consistent.
- **One base missing on disk:** logged once to stderr, then excluded from discovery. Non-fatal.
- **All bases missing:** the existing "no repos under" error fires, listing all configured bases for context.
- **Same repo cloned under two bases:** both surface in `--status` and the picker, distinguished by base label. We don't try to dedupe — that's user-visible state.

## 7. Out of scope (follow-ups)

- Picker keybindings for filtering by base.
- A `--base <dir>` flag for per-invocation override (#5).
- Migration helper to move a repo from one base to another.
- Updating `clrepo-watcher.sh` / `clrepo-autosync.sh` to iterate bases.

## 8. Open questions

1. Should the picker label use the auto-derived form or accept a user-supplied alias from the config file? **Tentative:** auto-derived in v1; aliases as follow-up.
2. Where does the picker render the label — left column, right column, parenthetical suffix? **Tentative:** left column, fixed-width, only when multi-base.
3. What happens with `clrepo update` when the clrepo source repo lives under one of the configured bases? **Tentative:** unchanged — `_clrepo_update` reads `_CLREPO_DIR` directly, not via `_CLREPO_BASES`.

## 9. Test plan

- Unit-ish: a Bash test like `tests/test_norm_path.sh` covering: parse `:`-separated env var, parse multi-line config file, expand `~`, dedupe, drop trailing `/`, drop missing dirs.
- Integration on the dev box: configure two bases under `/tmp/` with a sample repo each, run `clrepo --status`, `clrepo --pick`, `clrepo .` from inside each tree.
- Single-base regression: every test that passes today must still pass.
