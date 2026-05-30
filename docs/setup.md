# Bridge setup guide

End-to-end setup for **using** `bridge` (cloning repos, opening agent sessions)
and **developing** it — tab completion, repo cloning, and Claude Code launching
in tmux.

The core steps are the same on any Unix-like host. Per-OS package commands are
in the table below, and host-specific notes (Chromebook, macOS, other Linux,
Windows) are in [Environment-specific notes](#environment-specific-notes).

> Commands use **`just`** (the repo's task runner). `just` recipes wrap the
> underlying build steps, so you never call `make` directly.

---

## Prerequisites — system tools

Install: **git, tmux, fzf, direnv, bash-completion, curl**.

| Tool | Role |
|---|---|
| git | clone / repo operations |
| tmux | Claude Code sessions run inside it (Unix) |
| fzf | the repo picker |
| direnv | loads per-directory forge tokens for remote listing/cloning |
| bash-completion | rich tab completion (without it: `_get_comp_words_by_ref: command not found`) |
| curl | toolchain / CLI installers |

| OS | Install command |
|---|---|
| Debian / Ubuntu / **Crostini** | `sudo apt update && sudo apt install -y git tmux fzf direnv bash-completion curl` |
| Fedora | `sudo dnf install -y git tmux fzf direnv bash-completion curl` |
| Arch | `sudo pacman -S --needed git tmux fzf direnv bash-completion curl` |
| macOS (Homebrew) | `brew install git tmux fzf direnv bash-completion@2` |

Make sure your local bin dirs are on `PATH`:

```bash
mkdir -p ~/.local/bin
grep -q '.local/bin' ~/.bashrc || echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

---

## 1. Go toolchain (for building bridge)

> The `just install-go-toolchain` recipe runs **from the cloned repo**, so do
> [step 3 (clone bridge)](#3-install-bridge-binary--shim) first if you're going
> top-to-bottom, then come back here.

Bridge needs the Go version pinned in `go.mod` (currently **1.25.0**). The repo
fetches the exact version into `~/.local/go` for you (works on x86_64 and
arm64, Linux and macOS):

```bash
# from the cloned repo dir (step 3):
just install-go-toolchain
echo 'export PATH="$HOME/.local/go/bin:$PATH"' >> ~/.bashrc
exec bash -l
go version            # → go1.25.0 ...
```

Install `just` first if missing: `apt/dnf/pacman install just`, or
`brew install just`. (If your distro already ships Go ≥ 1.25 you can skip this
and use the system Go.)

---

## 2. Claude Code CLI

Install the `claude` CLI and authenticate once (use the current official
installer for your platform; x86_64 and arm64 are supported):

```bash
curl -fsSL https://claude.ai/install.sh | bash      # example installer
exec bash -l
claude            # first run walks you through /login; quit with Ctrl-C
```

`claude` must be on `PATH` for bridge to launch it.

---

## 3. Install bridge (binary + shim)

One clone serves both roles — you **use** bridge from here and **develop** it
here.

```bash
git clone https://github.com/freaxnx01/bridge \
  ~/projects/repos/github/freaxnx01/public/bridge
cd ~/projects/repos/github/freaxnx01/public/bridge

just build        # builds the Go binary → ~/.local/bin/bridge, installs the
                  # shim, prints the version (also git-pulls latest first)
```

---

## 4. Shell wiring (`bridge init`)

Wire the shim + completion into your shell (idempotent — safe to re-run):

```bash
cp ~/.bashrc ~/.bashrc.bak-bridge-$(date -u +%Y%m%d-%H%M%S)
bridge init                       # adds shim + completion source lines, and
                                  # seeds BRIDGE_DEFAULT_AGENT=claude with
                                  # --remote-control --dangerously-skip-permissions
bridge init --alias=br,brg        # optional: completion on your aliases
```

Other shells: `bridge init` auto-detects bash; for zsh/fish source
`bridge completion zsh|fish`; for PowerShell run `bridge init --shell powershell`
(see [Windows](#windows-powershell)).

Reload and smoke-test in a **fresh** shell:

```bash
exec bash -l
bridge doctor          # → all PASS (repo-meta cache may WARN; harmless)
type bridge            # → "bridge is a function"
bridge --version       # → bridge v2.x.y (commit ..., built ...)
```

`bridge doctor` is the source of truth — fix anything not PASS before moving on.

---

## 5. Repo discovery & cloning

### Where bridge looks for repos

Default root is `~/projects/repos`. Precedence (first non-empty wins):

1. `-B/--base <dir>[,<dir>]` (per-invocation)
2. `BRIDGE_BASE` env (`:`-separated; multiple roots)
3. `BRIDGE_REPOS_ROOT` env (single root)
4. `$XDG_CONFIG_HOME/bridge/base` (one path per line)
5. `~/projects/repos` (default)

Local repos are picked up automatically — `bridge` (bare) or `bridge <name>`.

### Listing & cloning remote repos (forge tokens + direnv)

Remote listing/cloning reads a forge token **scoped per directory** via
`direnv`. First hook direnv into the shell:

```bash
grep -q 'direnv hook bash' ~/.bashrc || echo 'eval "$(direnv hook bash)"' >> ~/.bashrc
exec bash -l
```

Drop an `.envrc` at the directory that scopes a forge and export its token.
Supported forges:

| Forge | Env var(s) |
|---|---|
| GitHub | `GH_TOKEN` (or `GITHUB_TOKEN`) |
| Forgejo | `FORGEJO_TOKEN` |
| GitLab | `GITLAB_TOKEN` |
| Azure DevOps | `AZURE_DEVOPS_ORG_URL` + `AZURE_DEVOPS_EXT_PAT` (or `ADO_PAT`) |

Example (GitHub scope):

```bash
mkdir -p ~/projects/repos/github/freaxnx01
cd ~/projects/repos/github/freaxnx01
echo 'export GH_TOKEN=ghp_xxxxxxxxxxxx' > .envrc
direnv allow
```

Then:

```bash
bridge -r            # picker over local + remote repos (cached)
bridge --refresh     # force-refresh the remote cache first (network-bound)
```

A remote-only repo shows as `↓ <name>`; selecting it clones via
`direnv exec <dir> git clone …` (right token loaded) and drops you into a
session. `bridge <partial><TAB>` completes local repo names.

---

## 6. Launching the agent (tmux on Unix)

With `BRIDGE_DEFAULT_AGENT` seeded by `bridge init`, opening a repo launches a
**tmux-wrapped, named** Claude session:

```bash
bridge <repo>
# → tmux new-session -A -s <repo> -c <repo-dir> \
#     claude -n <repo> --remote-control --dangerously-skip-permissions
```

- the **tmux** session is named after the repo (`-s <repo>`)
- the **Claude** session display name is the repo (`-n <repo>`), or
  `<repo> [<worktree>]` with `-w <name>`
- **`--remote-control`** lets you steer the session from claude.ai/code or the
  mobile app — the primary way to reach it off-device

Reattach / inspect:

```bash
bridge <repo>            # `new-session -A` re-attaches if it exists
bridge -a                # picker over live sessions
bridge sessions          # list live sessions
bridge <repo> -w feat-x  # isolated worktree session: <repo>/.worktrees/feat-x
```

(On Windows the launcher uses a Windows Terminal tab instead of tmux — see
[Windows](#windows-powershell).)

---

## 7. Developing bridge

```bash
cd ~/projects/repos/github/freaxnx01/public/bridge

go test ./...        # full Go test suite (fast iteration; no install needed)
just test            # Go + shim (bats) tests
just test-verbose    # go test -v ./...
just build           # rebuild + reinstall the live binary (git-pulls first),
                     # then prints the version
```

> `just build` runs `git pull` before installing — ideal for updating from
> `main`. While iterating on a feature branch, run `go test ./...` to verify and
> `just build` when you want to refresh the installed binary (commit/stash
> local work first so the pull fast-forwards cleanly).

The shim `bats` tests need `bats` installed (`apt/dnf/pacman/brew install bats`).
After changing the **shim** itself (rare — `git diff -- shims/`), reinstall and
open a new terminal (the running shell still has the old shim function loaded).

**Working across sessions:** see *Working across sessions* in `CLAUDE.md` —
fetch before branching, push on commit, isolate concurrent work with
`bridge -w`, delete branches on merge.

---

## 8. Verify everything

```bash
exec bash -l
bridge doctor                 # all PASS
bridge <repo-prefix><TAB>     # tab completion resolves a repo (no error)
br <TAB>                       # alias completion (if you wired --alias)
bridge -r                      # remote listing works (forge token loaded)
bridge <some-repo>             # lands in a tmux session running claude
tmux ls                        # shows the <repo> session
```

---

## Environment-specific notes

### Chromebook (ChromeOS / Crostini)

- Enable **Settings → Advanced → Developers → Linux development environment**;
  run everything in the **Terminal** app. The container is Debian-based, so use
  the `apt` row above.
- Works on both ARM64 and x86_64 Chromebooks — the toolchain (step 1) and the
  bridge binary build for your local arch automatically.
- ChromeOS passes the mouse through, so tmux mouse mode works once a session
  exists. The **Remote Control** URL is the off-device entry point — steer a
  running session from claude.ai/code or the mobile app rather than keeping the
  terminal foregrounded.

### macOS

- Use the Homebrew row above; if you run bash (not the default zsh), install
  `bash-completion@2` and follow its `brew info` instructions to source it.
- The launcher uses tmux exactly as on Linux. For zsh, source
  `bridge completion zsh`.

### Other Linux (Fedora / Arch / …)

- Use the matching package row above. Everything else (Go toolchain, install,
  init, tmux launch) is identical — bridge is a static Go binary plus a bash
  shim.

### Windows (PowerShell)

- bridge runs natively under PowerShell: cross-compile or install a
  `bridge.exe` on `PATH`, dot-source `shims/bridge-shim.ps1` from your
  `$PROFILE`, and run `bridge init --shell powershell`. The launcher uses a
  **Windows Terminal** tab (`wt.exe`) instead of tmux. Full details and the
  exact lines are in [`go-migrate.md`](../go-migrate.md) → *Cross-platform
  notes*. There is no Windows CI; the path is exercised manually.

---

## Troubleshooting

- **`_get_comp_words_by_ref: command not found` on TAB** → `bash-completion`
  isn't loaded in this shell. Install it (prerequisites) and open a **new**
  terminal — a shell opened before the install won't have it.
- **`bridge` cd's but never launches the agent** → `BRIDGE_DEFAULT_AGENT` isn't
  set in the current shell. `echo $BRIDGE_DEFAULT_AGENT` should print `claude`;
  if empty, `exec bash -l`.
- **`bridge --version` looks wrong / "command not found"** → `~/.local/bin` not
  on `PATH`, or the shell didn't source the shim. Check `command -v bridge` →
  `~/.local/bin/bridge`.
- **`bridge -r` slow or fails** → forge network/token issue. `cd` into the
  scope dir and check `direnv status` / `echo $GH_TOKEN`.
- **Completion/shim works in some shells but not others** → only shells started
  after `bridge init` (or a shim reinstall) pick it up. Open a new terminal.

---

## Reference

- Install/update internals & the shim contract: [`go-migrate.md`](../go-migrate.md)
- CLI surface, cache files, package map: [`README.md`](../README.md)
- Cross-session git hygiene: `CLAUDE.md` → *Working across sessions*
