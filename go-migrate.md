# Go migration & update guide

`bridge` is a Go binary as of `v2.0.0` (2026-05-26). The bash code (`bridge.sh` and friends) is frozen in the repo but never sourced — it stays for one release cycle until Phase 4 deletes it (see [#35](https://github.com/freaxnx01/bridge/issues/35)).

After cutover, typing `bridge` invokes a tiny shell function (the shim) that calls the Go binary, parses a directive from its `__preflight` output, and acts on it (`cd:`, `exec:`, or pass-through). No daemon, no IPC — stateless per invocation.

## Two states to distinguish

- **Cut over** — `~/.bashrc` sources `~/.local/share/bridge/bridge-shim.sh`. The shim calls the Go binary at `~/.local/bin/bridge`. This is the post-`v2.0.0` state on a fully migrated machine.
- **Not cut over** — `~/.bashrc` still sources `bridge.sh`. Needs the one-time activation below.

`bridge --version` tells you which mode you're in: it'll print `bridge v2.x.y (...)` from the Go binary, or it'll be a shell function from `bridge.sh`.

## Updating an already-cut-over machine

```bash
cd ~/projects/repos/github/freaxnx01/public/bridge
git pull
make install-go
```

That's it. The Makefile's `build-go` target injects `git describe --tags --always --dirty` into the binary via `-ldflags`, so `bridge --version` reflects the new commit immediately. No `.bashrc` change, no shim reinstall.

If the shim *itself* changed in the pull (rare — check `git diff HEAD@{1} -- shims/`):

```bash
make install-shim
```

Then **open a new terminal**. The existing shell still has the previous shim function loaded from when `.bashrc` was sourced; only a fresh shell picks up the new shim file.

## Activating on a fresh machine (one-time)

```bash
git clone https://github.com/freaxnx01/bridge ~/projects/repos/github/freaxnx01/public/bridge
cd ~/projects/repos/github/freaxnx01/public/bridge
make install-go install-shim

# Back up .bashrc:
cp ~/.bashrc ~/.bashrc.bak-bridge-cutover-$(date -u +%Y%m%d-%H%M%S)
```

Then edit `~/.bashrc`. If a previous bash bridge source line exists, replace it:

```sh
# from:
_f=~/projects/repos/github/freaxnx01/public/bridge/bridge.sh; [ -f "$_f" ] && . "$_f"; unset _f
# to:
_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
```

If this is a brand-new install with no prior bridge source line, just append:

```bash
echo '_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f' >> ~/.bashrc
```

Open a new terminal and smoke-test:

```bash
type bridge          # → "bridge is a function"
declare -f bridge    # → should show the shim body (case "$directive" in ...)
bridge --version     # → "bridge v2.x.y (commit ..., built ...)"
bridge list | head   # → repo listing
bridge bridge        # → cd's into the bridge repo
```

## What runs after the cutover

| Component | Path | Role |
|---|---|---|
| Shell function `bridge` | `~/.local/share/bridge/bridge-shim.sh` (sourced into shell) | Interactive entry point. Runs `command bridge __preflight "$@"`, parses the directive, acts on it. |
| Go binary | `~/.local/bin/bridge` (~10 MB ELF) | All actual logic — discovery, picker, launcher, daemons, JSON output. |
| Cache | `~/.cache/bridge/` | `mru`, `slots.json`, `presence.json`, `sync.json`, `issues.json`, `repo-meta.json`, `remote.list`, `bridge.log`. Written by the Go binary; read-compat with the legacy bash `slots.json` shape for one release cycle. |
| Bash `bridge.sh` and friends | Repo on disk | **Frozen.** Not sourced. Scheduled for deletion in Phase 4. |

## Cross-platform notes

- Linux: `make install-go` writes a Linux ELF; the shim is bash. Launcher uses `tmux`.
- Windows: cross-compile via `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`, install the resulting `.exe` somewhere on PATH as `bridge.exe`, dot-source `shims/bridge-shim.ps1` from your `$PROFILE`. Launcher uses Windows Terminal (`wt.exe new-tab`). There is no Windows CI; the binary builds clean but the runtime path is exercised manually.

## Rollback

Any machine, any time:

```bash
# 1. Edit ~/.bashrc back to the bash source line:
sed -i 's|_f=~/.local/share/bridge/bridge-shim.sh|_f=~/projects/repos/github/freaxnx01/public/bridge/bridge.sh|' ~/.bashrc

# 2. Open a new terminal.
```

You're back on bash bridge. No data loss: the cache is forward-compatible — the Go binary writes `slots.json` as `{"slots":[...]}`, which the bash bridge treats as malformed and falls back to an empty registry (so worst case is one stale-slot blip until the next launch repopulates it). All other state files (`mru`, `presence.json`, `sync.json`, etc.) round-trip cleanly between the two.

## What the shim does (full source)

The shim is ≤20 lines on purpose — all real logic lives in the binary. From `shims/bridge-shim.sh`:

```sh
bridge() {
    local directive rc
    directive=$(command bridge __preflight "$@")
    rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        exec:*) eval "exec ${directive#exec:}" ;;
        noop)   command bridge "$@" ;;
        *)
            printf 'bridge: unknown directive: %s\n' "$directive" >&2
            return 1
            ;;
    esac
}
```

The `eval exec` is deliberate: the Go binary's `internal/shellbridge` emits sh-quoted argv (so an agent arg like `'echo hi there'` is a single argument), and `eval` is what re-parses those quotes. There's a bats test that round-trips a multi-word `exec:` arg to prove this works (see `shims/bridge-shim.bats`).

## When to worry

- `bridge --version` prints something that doesn't look like a Go version string → the binary isn't on PATH, or `~/.bashrc` is still sourcing `bridge.sh`. Check `command -v bridge` should resolve to `~/.local/bin/bridge`.
- `bridge` works in some shells but not others → only the shells started after the `.bashrc` edit see the new shim. Open a new terminal.
- `bridge list` is fast but `bridge -r` is slow / fails → the remote-listing path hits GitHub/GitLab/Forgejo. Network or token issue, not a migration issue. See `bridge issues --json` source paths for the expected env vars (`GH_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`).
- `bridge slots` shows a stale entry from a long-dead session → known gap, [#39](https://github.com/freaxnx01/bridge/issues/39).
- Anything else: roll back as above, open an issue, attach `~/.cache/bridge/bridge.log` (rotated, JSON-lines).
