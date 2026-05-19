# clrepo multi-base — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `_CLREPO_BASE` be a list of root directories instead of a single path, with all discovery / status / picker / cwd-launch surfaces spanning every base. Single-base setups stay byte-identical to today's output.

**Architecture:** `_CLREPO_BASES` is a new Bash array; `_CLREPO_BASE` is retained as `_CLREPO_BASES[0]` for backward compat. Sources resolved in the same precedence order as #3: env var (`:`-separated) > config file (one path per line) > default. Call sites grouped into discovery (iterate every base), CWD→rel (find owning base), and launch (use owning base).

**Spec:** [`docs/specs/2026-05-19-clrepo-multi-base-design.md`](../specs/2026-05-19-clrepo-multi-base-design.md) (issue #4)

**Hard prereq:** PR #13 (#3) merged. Without #3's config-file parsing on main, this plan starts from a fragile base — see Task 0.

---

### Task 0: Confirm prerequisites and starting state

- [ ] **Step 1: Verify PR #13 is merged on `main`.**

  Run: `git fetch && git log origin/main --oneline | grep -i 'config file'`. Expect the #13 squash-merge commit. If absent, STOP and surface to the user — implementing #4 against unmerged #3 creates branch-coupling pain.

- [ ] **Step 2: Verify version baseline.**

  Run: `grep '^_CLREPO_VERSION=' clrepo.sh`. Note the value — this PR bumps it.

- [ ] **Step 3: Branch off main.**

  ```bash
  git checkout main && git pull
  git checkout -b ralph/issue-4-multi-base-dirs
  ```

---

### Task 1: Tests for the parser

**Files:**
- Create: `tests/test_multi_base.sh`

Mirror the style of `tests/test_norm_path.sh`. Cover:

- env var `:`-split (zero / one / two / three values, ignoring empty elements)
- config-file multi-line parsing (blank lines, `#` comments, indentation, `~` and `$HOME` expansion)
- precedence (env wins over file wins over default)
- dedupe (same path twice → one element)
- trailing-`/` normalisation
- missing-dir handling (drop with a one-shot warning, non-fatal)

- [ ] **Step 1: Write the failing tests file.**
- [ ] **Step 2: Run `bash tests/test_multi_base.sh` — expect FAIL.**
- [ ] **Step 3: Commit: `test: add failing tests for multi-base parser`.**

---

### Task 2: Implement the parser

**Files:**
- Modify: `clrepo.sh` (the base-dir resolution block from #3)

- [ ] **Step 1: Refactor the resolution block.**

  After #3, the block looks like:

  ```bash
  _clrepo_read_base_file() { … }
  if [ -n "${CLREPO_BASE:-}" ]; then
    _CLREPO_BASE="$CLREPO_BASE"
  elif _CLREPO_BASE=$(_clrepo_read_base_file 2>/dev/null); then
    :
  else
    _CLREPO_BASE="$HOME/projects/repos"
  fi
  ```

  Becomes:

  ```bash
  _clrepo_read_base_file_all() {
    # Echoes every non-empty, non-`#` line from $_CLREPO_CONFIG/base,
    # with ~ and $HOME expanded.
    local f="$_CLREPO_CONFIG/base" line
    [ -r "$f" ] || return 1
    while IFS= read -r line || [ -n "$line" ]; do
      line="${line#"${line%%[![:space:]]*}"}"
      line="${line%"${line##*[![:space:]]}"}"
      [ -z "$line" ] && continue
      case "$line" in '#'*) continue ;; esac
      line="${line/#\~/$HOME}"
      line="${line//\$HOME/$HOME}"
      printf '%s\n' "$line"
    done < "$f"
    return 0
  }

  _CLREPO_BASES=()
  _clrepo_collect_bases() {
    local raw seen=""
    if [ -n "${CLREPO_BASE:-}" ]; then
      IFS=':' read -r -a raw <<< "$CLREPO_BASE"
    else
      mapfile -t raw < <(_clrepo_read_base_file_all 2>/dev/null)
      [ "${#raw[@]}" -eq 0 ] && raw=("$HOME/projects/repos")
    fi
    for p in "${raw[@]}"; do
      [ -z "$p" ] && continue
      p="${p%/}"                        # strip trailing /
      p="${p/#\~/$HOME}"; p="${p//\$HOME/$HOME}"
      case ":$seen:" in *":$p:"*) continue ;; esac   # dedupe
      if [ ! -d "$p" ]; then
        _clrepo_warn "base dir missing, skipping: $p"
        continue
      fi
      _CLREPO_BASES+=("$p")
      seen="$seen:$p"
    done
    [ "${#_CLREPO_BASES[@]}" -eq 0 ] && _CLREPO_BASES=("$HOME/projects/repos")
    _CLREPO_BASE="${_CLREPO_BASES[0]}"
  }
  _clrepo_collect_bases
  ```

- [ ] **Step 2: Run `bash tests/test_multi_base.sh` — expect PASS.**
- [ ] **Step 3: Run `bash tests/test_norm_path.sh` — should still PASS (regression).**
- [ ] **Step 4: Commit: `feat(clrepo): parse multi-base CLREPO_BASE list from env + config file`.**

---

### Task 3: Discovery iterates every base

**Files:**
- Modify: `clrepo.sh`

Three discovery surfaces touch `find`:

- `_clrepo_targets()` (clrepo.sh:100) — walks `.envrc` files.
- `_clrepo_status_pick` / `clrepo --pick` repo listing (clrepo.sh:1579).
- `clrepo()`'s main `all=$(find …)` (around clrepo.sh:2625).

- [ ] **Step 1: Update `_clrepo_targets` to loop `_CLREPO_BASES`. Emit a 5th TSV column with the absolute base.**
- [ ] **Step 2: Update the other two `find` sites the same way.**
- [ ] **Step 3: Confirm every consumer of the 4-column TSV either ignores the 5th column or uses it. Affected callers: `_clrepo_fetch_target`, `_clrepo_doctor`, `_clrepo_issues`, the picker assembly.**
- [ ] **Step 4: Linux single-base smoke — `clrepo --status` output unchanged.**
- [ ] **Step 5: Linux multi-base smoke — set `CLREPO_BASE=/tmp/a:/tmp/b` (both empty dirs), confirm no "missing dir" warning and the listing is empty without crash.**
- [ ] **Step 6: Commit: `feat(clrepo): discovery walks every configured base`.**

---

### Task 4: CWD → rel resolution finds the owning base

**Files:**
- Modify: `clrepo.sh` (around line 2683 — the `${git_root#$_CLREPO_BASE/}` block).

- [ ] **Step 1: Replace the single-base prefix strip with a loop:**

  ```bash
  local git_root rel owning_base=""
  git_root=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null)
  if [ -n "$git_root" ]; then
    for b in "${_CLREPO_BASES[@]}"; do
      if [[ "$git_root" == "$b/"* ]]; then
        rel="${git_root#$b/}"
        owning_base="$b"
        break
      fi
    done
  fi
  ```

  Then pass `owning_base` to `_clrepo_launch` so it can `cd "$owning_base/$rel"` instead of `$_CLREPO_BASE/$rel`. Pragmatic alternative: have `_clrepo_launch` re-resolve the owning base from `rel` by trying each `$base/$rel` for `[ -d ]`.

- [ ] **Step 2: Smoke: from inside a repo under base #1, bare `clrepo` launches it. From inside a repo under base #2, same.**
- [ ] **Step 3: Commit: `feat(clrepo): CWD launch resolves the owning base across the list`.**

---

### Task 5: Launch / clone path uses the owning base

**Files:**
- Modify: `clrepo.sh` lines 136, 343, 405, 475, 547-550, 1471 (every `cd "$_CLREPO_BASE/$rel"` or path-build site).

For each, determine the owning base from the rel and the `_CLREPO_BASES` list. The cleanest abstraction:

```bash
_clrepo_base_for_rel() {
  local rel="$1" b
  for b in "${_CLREPO_BASES[@]}"; do
    [ -d "$b/$rel" ] && { printf '%s\n' "$b"; return 0; }
  done
  printf '%s\n' "$_CLREPO_BASES[0]"   # fall back; clone target
  return 1
}
```

For clone sites, the fall-back picks the first base (today's behaviour for new clones). Document that explicitly.

- [ ] **Step 1: Add the helper.**
- [ ] **Step 2: Rewrite each call site.**
- [ ] **Step 3: Linux single-base regression: every command still works the same.**
- [ ] **Step 4: Linux multi-base smoke: clone, launch, delete each work across bases.**
- [ ] **Step 5: Commit: `feat(clrepo): launch and clone paths use per-rel owning base`.**

---

### Task 6: Display labels when multi-base

**Files:**
- Modify: `clrepo.sh` (the picker assembly + `--status` row formatting).

- [ ] **Step 1: Compute auto-derived labels with collision resolution (see spec §4).**
- [ ] **Step 2: When `${#_CLREPO_BASES[@]} > 1`, prefix picker rows with `<label>:`.**
- [ ] **Step 3: Update "no targets discovered" messages to list all bases.**
- [ ] **Step 4: Single-base regression: no labels added when only one base.**
- [ ] **Step 5: Commit: `feat(clrepo): label rows by base in multi-base listings`.**

---

### Task 7: Help, README, version bump, CHANGELOG

- [ ] **Step 1: Extend `clrepo --help`'s "Base dir" section (added by #3) to document the list semantics — env `:`-separated, config file one-per-line.**
- [ ] **Step 2: Add a "Multiple base dirs" subsection to README under the existing config section.**
- [ ] **Step 3: Bump `_CLREPO_VERSION` per the parallel-Ralph-PR convention noted in NOTES.md (next available minor).**
- [ ] **Step 4: Add CHANGELOG entry.**
- [ ] **Step 5: Final regression sweep: `bash tests/test_multi_base.sh`, `bash tests/test_norm_path.sh`, `clrepo --help`, single-base `clrepo --status`.**
- [ ] **Step 6: Commit: `chore(release): bump to <vX.Y.Z> for multi-base support`.**

---

## Sizing & risk notes

- ~250-350 LoC change across `clrepo.sh`. Comparable to #8.
- Highest-risk surface: Task 5 (every `cd`/clone site). Mitigation: the `_clrepo_base_for_rel` helper centralises the logic.
- Picker display (Task 6) is cosmetic — can be cut from v1 if scope balloons, with single-base behaviour intact.
- Slot/Telegram/RC state keyed by absolute paths: no migration needed (per spec §5).
