# Install & update guide

`bridge` is a Go binary at `~/.local/bin/bridge` wrapped by a tiny shell-function shim (`~/.local/share/bridge/bridge-shim.sh`) sourced from `~/.bashrc`. Typing `bridge` invokes the shim, which calls `command bridge __preflight "$@"`, parses a directive (`cd:`, `exec:`, or pass-through), and acts on it. No daemon, no IPC — stateless per invocation.

The legacy `bridge.sh` was deleted in v2.1.0 (Phase 4); the Go binary is the only implementation. Pre-cutover hosts (those still sourcing `bridge.sh` from `~/.bashrc`) should migrate per the steps below before pulling past v2.0.

## Updating an already-installed host

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

Then **open a new terminal**. The existing shell still has the previous shim function loaded; only a fresh shell picks up the new shim file.

## Fresh-install on a new host (one-time)

```bash
git clone https://github.com/freaxnx01/bridge ~/projects/repos/github/freaxnx01/public/bridge
cd ~/projects/repos/github/freaxnx01/public/bridge
make install                                # binary + shim + meta-augmenter
cp ~/.bashrc ~/.bashrc.bak-bridge-$(date -u +%Y%m%d-%H%M%S)
bridge init                                 # idempotently adds shim + completion source lines
```

Open a new terminal and smoke-test:

```bash
bridge doctor        # → all PASS (repo-meta.json cache may WARN; harmless)
type bridge          # → "bridge is a function"
bridge --version     # → "bridge v2.x.y (commit ..., built ...)"
bridge list | head   # → repo listing
bridge bridge        # → cd's into the bridge repo
```

## Migrating from a pre-v2.1 host still on bash bridge

If `~/.bashrc` still sources `bridge.sh`, replace that line **before** pulling past v2.0.0 (after that, the file no longer exists in the tree):

```bash
sed -i 's|_f=~/projects/repos/github/freaxnx01/public/bridge/bridge.sh|_f=~/.local/share/bridge/bridge-shim.sh|' ~/.bashrc
```

Then `make install-go install-shim`, open a new terminal, and smoke-test as above. The cache at `~/.cache/bridge/` is forward-compatible for `mru`, `presence.json`, `sync.json`, `repo-meta.json`, `remote.list`, and `slots.json` — `LoadSlots` auto-migrates a legacy bash-shape file by renaming it to `slots.json.legacy-<UTC>.bak` and starting fresh on the first Go invocation (#79).

## Tab completion for aliases (`brg`, etc.)

`bridge` registers bash completion under its own name only — user wrappers like `brg() { bridge "$@"; }` don't inherit it (#45). To get repo-name completion on an alias, source the cobra-generated completion and then point it at your wrappers:

```bash
# in ~/.bashrc, after the shim source line
eval "$(bridge completion bash)"
complete -o default -o nospace -F __start_bridge brg
```

(The flags + function name match what `bridge completion bash` registers for `bridge` itself — verify with `complete -p bridge` if your build differs.) zsh and fish have equivalent `bridge completion zsh` / `fish` outputs — wire similarly for their alias mechanisms.

## What runs on a configured host

| Component | Path | Role |
|---|---|---|
| Shell function `bridge` | `~/.local/share/bridge/bridge-shim.sh` (sourced into shell) | Interactive entry point. Runs `command bridge __preflight "$@"`, parses the directive, acts on it. |
| Go binary | `~/.local/bin/bridge` (~10 MB ELF) | All actual logic — discovery, picker, launcher, daemons, JSON output. |
| Cache | `~/.cache/bridge/` | `mru`, `slots.json`, `presence.json`, `sync.json`, `issues.json`, `repo-meta.json`, `remote.list`, `bridge.log`. |

## Cross-platform notes

- Linux: `make install-go` writes a Linux ELF; the shim is bash. Launcher uses `tmux`.
- Windows: cross-compile via `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`, install the resulting `.exe` somewhere on PATH as `bridge.exe`, dot-source `shims/bridge-shim.ps1` from your `$PROFILE`. Launcher uses Windows Terminal (`wt.exe new-tab`). There is no Windows CI; the binary builds clean but the runtime path is exercised manually.

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

The `eval exec` is deliberate: the Go binary's `internal/shellbridge` emits sh-quoted argv (so an agent arg like `'echo hi there'` is a single argument), and `eval` is what re-parses those quotes. A bats test in `shims/bridge-shim.bats` round-trips a multi-word `exec:` arg to prove this works.

## When to worry

- `bridge --version` prints something that doesn't look like a Go version string → the binary isn't on PATH, or `~/.bashrc` isn't sourcing the shim. Check `command -v bridge` resolves to `~/.local/bin/bridge`.
- `bridge` works in some shells but not others → only shells started after the `.bashrc` edit see the new shim. Open a new terminal.
- `bridge -r` is slow / fails → the remote path hits GitHub/GitLab/Forgejo. Network or token issue. Expected env vars: `GH_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`.
- `bridge slots` shows entries with no `*` (live marker) → those slots' tmux sessions are gone. Run `bridge slots prune` to drop them.
- Anything else: open an issue, attach `~/.cache/bridge/bridge.log` (rotated, JSON-lines).
