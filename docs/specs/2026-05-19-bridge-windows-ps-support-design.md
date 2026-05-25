# bridge on Windows / PowerShell — Design

- **Issue:** #8
- **Date:** 2026-05-19
- **Status:** Draft (brainstorm complete)

## 1. Goal & non-goals

**Goal.** A Windows user with a base directory like `C:\Develop\Repos` can install and use `bridge` from PowerShell, with the same feature surface as Linux/macOS users.

**Non-goals.**

- Native PowerShell port. `bridge.sh` stays canonical; PS users run it under Git Bash.
- WSL-specific paths or behaviour (works incidentally — not tested, not documented).
- PowerShell-native tab completion in v1 (Bash completion inside Git Bash still works).
- Any change to Azure DevOps support — it is already first-class in `bridge.sh` via the existing `forge` dispatch. The ADO section of #8 is obsolete.
- Anything owned by #3 (config file), #4 (multi-base), #5 (`--base` flag), #6 (per-repo issues), #7 (dashboard) — those land separately.

## 2. Architecture

Three layers, with the smallest possible Windows-specific surface:

```
PowerShell user           Linux/macOS user
     │                          │
     ▼                          │
bridge.ps1  ────► bash.exe ◄────┘ (Bash directly)
                     │
                     ▼
                 bridge.sh   ◄── canonical, one source of truth
                     │
                     ▼
             Path normalization layer
            (cygpath when on Windows; no-op elsewhere)
```

**Components.**

1. **`bridge.sh` (existing, modified).** Gains a small path-normalization helper (`_bridge_norm_path`) that runs `cygpath -u` on Windows-style inputs and is a no-op elsewhere. Called wherever `BRIDGE_BASE` (and any user-supplied path) enters the script.

2. **`bridge.ps1` (new, ~30 lines).** Single-purpose shim: locate `bash.exe` (Git Bash), forward args to `bash -c 'source bridge.sh && bridge "$@"'`, preserve exit code, stream stdout/stderr.

3. **README additions.** A "Windows / PowerShell" section: prerequisites (Git for Windows), how to load the shim, where to set `$env:BRIDGE_BASE` / `$env:GITHUB_TOKEN` / `$env:AZURE_DEVOPS_EXT_PAT`, the `cd` caveat (Section 5).

**Platform detection.** A single `_bridge_is_windows` helper checks `$OSTYPE` / `uname -s` for `MINGW*`, `MSYS*`, `CYGWIN*`. Everything else branches off that helper, not scattered `if`s.

## 3. Path & config behaviour

**`BRIDGE_BASE` accepts three forms, normalized at entry:**

| Input from user                | Normalized to        |
|--------------------------------|----------------------|
| `C:\Develop\Repos` (PS native) | `/c/Develop/Repos`   |
| `C:/Develop/Repos` (mixed)     | `/c/Develop/Repos`   |
| `/c/Develop/Repos` (POSIX)     | `/c/Develop/Repos`   |

Normalization happens once, right after `BRIDGE_BASE` is read. All downstream code keeps using POSIX paths — no per-callsite changes.

**Display paths.** When on Windows and printing a path the user might paste elsewhere (errors, `--help`, `bridge --list`), pass through `cygpath -w` so they see `C:\Develop\Repos\...`. Internal logs/debug stay POSIX. One helper, `_bridge_display_path`.

**Config location.** `$HOME/.config/bridge/` everywhere. Git Bash sets `$HOME` to `%USERPROFILE%`, so Windows users get `C:\Users\<name>\.config\bridge\` — no `%APPDATA%` branching, no second code path. Existing files (`ado-projects`, tokens, `repo-meta.json` cache) keep working unchanged.

**Tokens.** `$env:GITHUB_TOKEN` / `$env:AZURE_DEVOPS_EXT_PAT` set in PowerShell are inherited by `bash.exe` automatically. No special handling.

**Fallback if `cygpath` is missing.** Shouldn't happen (Git Bash ships it). If it is missing, use a tiny pure-Bash replacement that handles `[A-Za-z]:\…` → `/<lower>/…` and continue. No hard dependency on `cygpath`.

## 4. The PowerShell shim (`bridge.ps1`)

**Responsibilities, in order.**

1. **Locate `bash.exe`.** First hit wins:
   1. `$env:BRIDGE_BASH` (escape hatch).
   2. `git.exe --exec-path` → `..\..\bin\bash.exe` (most reliable; reuses the user's Git).
   3. `C:\Program Files\Git\bin\bash.exe`.
   4. `C:\Program Files (x86)\Git\bin\bash.exe`.
   5. `where.exe bash` (last resort; may be WSL bash — accept it, document caveat).

   If none found: `bridge: bash.exe not found. Install Git for Windows or set $env:BRIDGE_BASH.` exit code 127.

2. **Forward arguments faithfully.** `& $bash -c 'source "$BRIDGE_SH" && bridge "$@"' bridge @args`. The literal `bridge` is `$0`; `@args` becomes `$@` so quoting/spaces survive. `$BRIDGE_SH` is resolved from `$PSScriptRoot\bridge.sh` so users don't need to set anything.

3. **Stream output, preserve exit code.** No buffering, no munging. Mirror `$LASTEXITCODE`.

4. **Nothing else.** No flag parsing, no config reading, no path translation in PS — `bridge.sh` already normalizes. Keep the shim dumb.

**Loading.**

- One-off: `. C:\path\to\bridge.ps1 <args>`.
- Recommended: a one-liner in `$PROFILE`:
  ```powershell
  function bridge { & "C:\path\to\bridge.ps1" @args }
  ```

## 5. Testing & verification

**Linux/macOS regression (main risk).** Every change to `bridge.sh` is in a code path POSIX systems also hit. The normalization helper must be a strict no-op outside Windows.

- Smoke test: `BRIDGE_BASE=/tmp/repos bridge --version`, `bridge --list`, one repo operation. Existing behaviour unchanged.
- Unit-style: source `bridge.sh`, call `_bridge_norm_path` with sample inputs, assert equality when `_bridge_is_windows` is false.

**Windows verification (manual checklist).**

1. PowerShell, fresh terminal:
   ```powershell
   $env:BRIDGE_BASE = 'C:\Develop\Repos'
   $env:GITHUB_TOKEN = '...'
   . .\bridge.ps1 --version    # prints _BRIDGE_VERSION
   . .\bridge.ps1 --list       # lists repos under C:\Develop\Repos
   . .\bridge.ps1 <repo>       # cd's in the bash subshell (see caveat)
   ```
2. Repeat with `$env:BRIDGE_BASE = '/c/Develop/Repos'` — same results.
3. From Git Bash on the same box: `BRIDGE_BASE=/c/Develop/Repos bridge --list` — same results. Confirms one canonical script, two entry points.
4. ADO smoke: `bridge <existing-ado-repo>` resolves and clones if needed. Proves forge dispatch survives the shim.
5. Missing-bash failure: rename `bash.exe` temporarily, run shim, confirm clear error and exit 127.

**`cd` caveat.** `bridge`'s `cd` only affects the bash subprocess, not the parent PowerShell session. Known limitation of running Bash under PowerShell. Not addressed in v1 — documented in README. A future PowerShell-native wrapper could call `Set-Location` after running bridge; out of scope.

**No CI on Windows in v1.** The user is the test environment.

## 6. Out of scope / deferred

- PowerShell-native tab completion (`Register-ArgumentCompleter`). Bash completion still works in Git Bash.
- Making `bridge <repo>` change the PowerShell session's cwd. Needs a PS-native wrapper or an output protocol.
- Windows CI (`windows-latest` runner).
- `%APPDATA%`-based config layout. Revisit only if `$HOME/.config/bridge` causes a concrete problem.
- WSL as an alternative runtime. Works incidentally; not tested or documented.
- Native PowerShell port or binary rewrite. Reopen only if (a) ADO support diverges from GitHub and the case-branches get painful, or (b) the user pushes back on the Git Bash dependency.
- Interactions with #3 / #4 / #5 / #6 / #7. Each handles its own Windows concerns when it lands.
