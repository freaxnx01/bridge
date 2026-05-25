# Focus Flag — Full Implementation Design

- **Issue:** #9
- **Date:** 2026-05-20
- **Status:** Approved for implementation
- **Prior work:** MVP landed in PR #23 (GH-only list, add, rm; no counts, no cache, no Forgejo)

## Goal

Complete the remaining acceptance criteria from issue #9: Forgejo support, open-issue counts with "assigned to me", JSON caching with TTL, `--no-cache` bypass, `bridge -f <name>` open-by-name, tab completion over the focus list, and partial-failure handling.

## Approach

Monolithic expansion — all changes inside `bridge.sh`. Follows existing codebase pattern (one large self-contained helper per feature area, e.g. `_bridge_dashboard`). No new files except `tests/test_focus.sh`.

---

## 1. New constants (module load)

```bash
_BRIDGE_FOCUS_CACHE="$_BRIDGE_CACHE/focus.json"
_BRIDGE_FOCUS_TTL="${BRIDGE_FOCUS_TTL:-3600}"
```

Added alongside `_BRIDGE_REMOTE_TTL` and `_BRIDGE_UPDATE_TTL`.

---

## 2. Forgejo support

### `_bridge_focus_toggle_fj <rel> add|rm`

New helper, mirrors `_bridge_focus_toggle_gh`.

- `cd "$(_bridge_base_for_rel "$rel")/$rel"` — direnv walks up to `git-forgejo/.envrc`, loads `FORGEJO_TOKEN`.
- `owner=freax` (hardcoded, matching `_bridge_targets`).
- `name=$(basename "$rel")`.
- **Add:** `PUT https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/topics/focus`
- **Rm:** `DELETE https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/topics/focus`

Both return 204 on success. Idempotent (adding an existing topic or deleting a missing one is a no-op per Forgejo API).

### Dispatch in `_bridge_focus_add` / `_bridge_focus_rm`

```bash
git-forgejo/*) _bridge_focus_toggle_fj "$rel" add ;;   # was: deferred message
```

Both functions call `rm -f "$_BRIDGE_FOCUS_CACHE"` on success.

### Forgejo repo fetch in `_bridge_focus_list`

Find the first Forgejo target rel via `_bridge_targets | awk -F'\t' '$2=="forgejo"{print $1;exit}'`. If none exists, skip silently. Otherwise:

```bash
cd "$base/$fj_rel"; eval "$(direnv export bash 2>/dev/null)"
curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
  "https://git.home.freaxnx01.ch/api/v1/repos/search?topic=true&q=focus&limit=50" \
  | jq -r '.data[] | "FJ\t\(.full_name)\t\(.html_url)"' > "$tmpdir/fj"
```

If `FORGEJO_TOKEN` is empty or curl fails, the job exits 0 and appends a warning to a shared warnings list.

---

## 3. Issue counts + user resolution

**User resolution (once per list run, before the parallel count loop):**
- GH: `gh api user --jq .login` run in the first GH target's direnv context → `$gh_user`
- FJ: `curl .../api/v1/user | jq -r .login` in the Forgejo direnv context → `$fj_user`

If user resolution fails, `$gh_user` / `$fj_user` stay empty and "mine" counts are omitted from output.

**Per-repo count jobs (second parallel fan-out, after repos are known):**

GH:
```bash
gh issue list --repo "$nwo" --state open --json number,assignees --limit 100 \
  | jq -r --arg me "$gh_user" '
      (length | tostring) + " " +
      ([.[] | select(any(.assignees[]; .login == $me))] | length | tostring)
    ' > "$tmpdir/count_$i"
```

FJ:
```bash
curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
  "https://git.home.freaxnx01.ch/api/v1/repos/$owner/$name/issues?state=open&type=issues&limit=50" \
  | jq -r --arg me "$fj_user" '
      (length | tostring) + " " +
      ([.[] | select(.assignee.login == $me)] | length | tostring)
    ' > "$tmpdir/count_$i"
```

If a count job fails, `$tmpdir/count_$i` is absent or empty → display `? open` for that repo.

**Aggregation after `wait`:** join repo TSV rows with count files by index. Accumulate totals for the summary footer.

---

## 4. Caching

**Cache schema** (`~/.cache/bridge/focus.json`):
```json
{
  "fetched_at": 1716210000,
  "repos": [
    { "platform": "GH", "name": "owner/repo", "url": "https://...", "open": 3, "mine": 1 },
    { "platform": "FJ", "name": "freax/repo", "url": "https://...", "open": 5, "mine": 2 }
  ],
  "warnings": []
}
```

`fetched_at` is a Unix timestamp (`date +%s`).

**Read path:**
```bash
if [ -f "$_BRIDGE_FOCUS_CACHE" ] && [ "$1" != "1" ]; then
  age=$(( $(date +%s) - $(jq '.fetched_at' "$_BRIDGE_FOCUS_CACHE") ))
  if [ "$age" -lt "$_BRIDGE_FOCUS_TTL" ]; then
    _bridge_focus_display_cache  # reads $_BRIDGE_FOCUS_CACHE, renders Section 7 format
    return
  fi
fi
```

**Write path:** after all jobs complete, assemble JSON with `jq -n`, write to `$_BRIDGE_FOCUS_CACHE.tmp`, then `mv -f` in place. Atomic — a crashed run leaves no half-written cache.

**Invalidation:** `rm -f "$_BRIDGE_FOCUS_CACHE"` on successful `--focus-add` or `--focus-rm`.

**`force_refresh` param:** `_bridge_focus_list` accepts `$1=1` to skip the cache read (used by `--no-cache`).

---

## 5. `-f` parsing change + open-by-name

**Before:**
```bash
-f|--focus-list) _bridge_focus_list; return ;;
```

**After:**
```bash
-f|--focus-list) mode_focus=1; shift ;;
--no-cache)      focus_no_cache=1; shift ;;
```

`mode_focus` and `focus_no_cache` added to the `local` declarations in `bridge()`. `--no-cache` outside `mode_focus` is silently ignored.

**After the dispatch loop**, before `mode_repo_issues`:
```bash
if [ "$mode_focus" = 1 ]; then
  if [ -z "${1:-}" ]; then
    _bridge_focus_list "$focus_no_cache"
    return
  fi
  # name given → fall through to the existing positional-arg launch path
fi
```

When a name is given, execution falls through to the existing exact → substring → metadata lookup and `_bridge_launch`. Resolution is against all local repos (not focus-only); tab completion is what constrains the suggestions to focus repos.

**Conflict checks:** `mode_focus` added to the bad-flags list in the `--attach` and `--pick` guards.

---

## 6. Tab completion

`bridge -f <TAB>` completes from cached focus basenames (no API call):

```bash
if [ "${COMP_WORDS[COMP_CWORD-1]}" = "-f" ] || \
   [ "${COMP_WORDS[COMP_CWORD-1]}" = "--focus-list" ]; then
  if [ -f "$_BRIDGE_FOCUS_CACHE" ]; then
    local focus_names
    focus_names=$(jq -r '.repos[].name | split("/")[-1]' \
                  "$_BRIDGE_FOCUS_CACHE" 2>/dev/null)
    COMPREPLY=($(compgen -W "$focus_names" -- "$cur"))
    return
  fi
fi
```

Falls back to all-repo completion if cache is absent. `--no-cache` added to the flag string.

---

## 7. Output format

```
FOCUS REPOS
────────────────────────────────────────────────────────
[GH]  owner/repo-name              3 open · 1 yours
      https://github.com/owner/repo-name
[FJ]  freax/homelab-ansible        5 open · 2 yours
      https://git.home.freaxnx01.ch/freax/homelab-ansible
[GH]  owner/other-repo             ? open
      https://github.com/owner/other-repo
────────────────────────────────────────────────────────
3 focus repos · 8 open issues · 3 assigned to you

  [!] Forgejo: skipped (no FORGEJO_TOKEN)
```

- `? open` when a count job failed.
- `· N yours` omitted when user resolution failed or count is 0.
- Summary footer omits "assigned to you" when `$gh_user` and `$fj_user` are both unknown.
- Warning lines from the `warnings` cache field printed after the footer, indented with `[!]`.

---

## 8. Partial-failure handling

| Failure | Behaviour |
|---|---|
| Forgejo unreachable (curl error) | GH results shown; `"Forgejo: skipped (curl error)"` in warnings |
| No `FORGEJO_TOKEN` | GH results shown; `"Forgejo: skipped (no FORGEJO_TOKEN)"` in warnings |
| No Forgejo target in `_bridge_targets` | Forgejo fetch silently skipped; no warning |
| Per-repo GH count failure | Row shows `? open`; total uses `?` for that repo |
| GH user resolution failure | `mine` counts omitted from all GH rows |

---

## 9. Testing (`tests/test_focus.sh`)

Self-contained Bash assertions, no framework. Tests:

1. **Cache read/write round-trip** — write a synthetic `focus.json`, source `bridge.sh`, call `_bridge_focus_display_cache`, assert output matches expected format.
2. **TTL expiry** — write a `focus.json` with `fetched_at` set to `now - TTL - 1`, assert cache is not used (fall-through to fetch).
3. **`--no-cache` bypass** — write a valid `focus.json`, call `_bridge_focus_list 1`, assert cache is not read (mock fetch returns empty).
4. **Invalidation on add/rm** — write a `focus.json`, stub `_bridge_focus_toggle_gh` to succeed, call `_bridge_focus_add`, assert cache file is absent.
5. **Partial-failure warning** — write a `focus.json` with a non-empty `warnings` field, assert warning line appears in display output.

---

## Version

Minor bump: `1.40.1 → 1.41.0` (new feature). Matching CHANGELOG entry.
