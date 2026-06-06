# bridge create-repo + bot /newrepo — design

**Status:** approved
**Date:** 2026-06-07

## Problem

You can start/manage sessions from Telegram (`bridge-bot`) and open existing
repos (`bridge open`), but there is no way to **create a new repo**. `bridge`
has no create command (open issues #88 `feat(forge): create remote repo`, #129
`Ctrl+N new repo, choose forge, GitHub Private|Public`). We want to create a new
repo from the control bot: `/newrepo <name>` → pick forge + visibility → the
repo is created and cloned locally, then the bot offers to launch a session.

## Decisions (from brainstorming)

- **Where:** repo-creation lives in `bridge` as a real `bridge create`
  subcommand (Go); the bot shells out to it. Closes #88 for Forgejo + GitHub.
- **Forges:** **Forgejo** (`git.home.freaxnx01.ch`) and **GitHub** (`freaxnx01`).
  GitLab later.
- **Visibility:** both **private and public** supported per forge. CLI default
  is private (`--public` overrides); the bot always asks via buttons.
- **Post-create:** create + clone, then the bot *offers* a launch button
  (does not auto-launch).

## Local layout & token resolution (existing conventions)

`bridge create` must place the clone where `bridge list/open` already discover
repos, using the same per-target `.envrc`/direnv token resolution:

| Forge | Clone target | Token `.envrc` dir | Token var |
| --- | --- | --- | --- |
| forgejo | `<root>/git-forgejo/<name>` | `<root>/git-forgejo` | `FORGEJO_TOKEN` |
| github | `<root>/github/<owner>/<vis>/<name>` | `<root>/github/<owner>/<vis>` | `GH_TOKEN` (→ `GITHUB_TOKEN`) |

`<root>` = first `reposRoots()` entry containing the forge dir; `<owner>` =
`freaxnx01`; `<vis>` = `public`|`private`. GitHub stores private vs public repos
under separate dirs **each with its own PAT** (private PAT can't be used for the
public tree and vice-versa), so the token dir is visibility-specific. Token
read via the existing `envFromDirenv(dir, vars)`.

## Part A — `bridge create` (Go)

### Forge clients (`internal/forge/`)

Add a `post(ctx, path, body, out)` helper to each client mirroring its `get()`
(Forgejo: `Authorization: token …`; GitHub: `Authorization: Bearer …`;
`Content-Type: application/json`; non-2xx → typed error; 409 → `ErrRepoExists`):

```go
// forgejo.go
func (c *ForgejoClient) CreateRepo(ctx, name string, private bool) (RepoRef, error)
//   POST /api/v1/user/repos  {name, private, auto_init:true, default_branch:"main"}

// github.go
func (c *GithubClient) CreateRepo(ctx, name string, private bool) (RepoRef, error)
//   POST /user/repos        {name, private, auto_init:true}
```

`auto_init:true` gives an initial commit so the repo is immediately clonable.
`RepoRef` gains `SSHURL`/`CloneURL`/`HTMLURL` if not already present; clone uses
`SSHURL` (matches existing SSH remotes).

### `cmd/bridge/create.go` (new)

```
bridge create <name> [--forge forgejo|github] [--public] [--json]
```

1. Validate `name` against `^[A-Za-z0-9._-]+$`.
2. `--forge` default `forgejo`. `private := !--public`.
3. Resolve target dir + token for the (forge, visibility) per the table above;
   missing token → clear error naming the expected `.envrc`.
4. Build the client (`NewForgejoClient`/`NewGithubClient` with the matching
   `BRIDGE_*_API` override) and `CreateRepo(ctx, name, private)`.
5. Clone `git clone <ref.SSHURL> <cloneTarget>` via a package-level
   `var cloneFn = runGitClone` seam (mockable in tests).
6. Output:
   - human: `✅ created <full_name> (<vis>, <forge>) → <path>`
   - `--json`: `{"name","full_name","forge","private","path","html_url"}`

**Error handling (distinct):** invalid name → usage, nothing created; no token →
"no <FORGE> token (check <dir>/.envrc)"; 409 → "repo <name> already exists";
remote created but clone failed → report repo + target path + manual clone hint,
exit non-zero, do NOT delete the remote.

Registered in `main.go`.

## Part B — bot `/newrepo`

### Flow (matches issue #129)

`/newrepo <name>` →  the bot validates the name, then shows a 2×2 inline
keyboard; nothing is created until a button is tapped:

```
Create "<name>" where?
[ Forgejo · Private ] [ Forgejo · Public ]
[ GitHub  · Private ] [ GitHub  · Public ]
```

Tapping a button (`callback_data = newrepo:<forge>:<vis>:<name>`) runs
`bridge create <name> --forge <forge> [--public] --json`, then edits the message
to **"✅ Created + cloned: `<full_name>`"** with a single inline button
**🚀 Launch session** (`callback_data = newrepo_launch:<name>`). Tapping that
runs the existing spawner (`bridge open <name> --agent claude --rc`).

### `bridge-bot/handlers.py`

- `Context` gains `repo_creator: Callable[[str, str, bool], dict | None]`
  `(name, forge, private) -> parsed json | None`.
- `cmd_newrepo(ctx, chat_id, args)`: empty/invalid name → usage; else send the
  2×2 keyboard.
- `on_callback`:
  - `newrepo:<forge>:<vis>:<name>` → `repo_creator(name, forge, vis=="private")`;
    success → edit to created + Launch button; failure → edit with error.
  - `newrepo_launch:<name>` → `spawner(name, [])`, edit with launch result.
- Name re-validated server-side in the callback (defense in depth).

### `bridge-bot/bridge_bot.py`

- `_create_repo(name, forge, private) -> dict | None`: shell
  `bridge create <name> --forge <forge> [--public] --json`, parse stdout JSON;
  None + log on error.
- Wire `repo_creator=_create_repo` into `build_context`.
- Dispatch `/newrepo` in `_handle_message`.
- `BOT_COMMANDS`: add `{"command":"newrepo","description":"Create a new repo (Forgejo/GitHub)"}`.

## Components / files

| File | Change |
| --- | --- |
| `internal/forge/forgejo.go` | +`post()`, +`CreateRepo`, +`ErrRepoExists`, RepoRef url fields |
| `internal/forge/github.go` | +`post()`, +`CreateRepo` (shares `ErrRepoExists`) |
| `internal/forge/forgejo_test.go` / `github_test.go` | CreateRepo via httptest (private body, auto_init, 409, parse) |
| `cmd/bridge/create.go` | new command: forge/visibility resolution, token, `cloneFn` seam |
| `cmd/bridge/create_test.go` | name validation, forge+vis→dir/token mapping, `--public`, clone seam, `--json` |
| `cmd/bridge/main.go` | register `create` |
| `bridge-bot/handlers.py` | `cmd_newrepo` + `newrepo:`/`newrepo_launch:` callbacks + `repo_creator` |
| `bridge-bot/bridge_bot.py` | `_create_repo`, dispatch, `BOT_COMMANDS` entry |
| `bridge-bot/tests/test_handlers.py` (+ entrypoint test) | keyboard, create callback, launch callback, usage |

## Testing

- **Go:** `forgejo_test.go` + `github_test.go` — assert POST path/body
  (`private`, `auto_init`) and response→RepoRef mapping; 409 → `ErrRepoExists`.
  `create_test.go` — name validation; (forge, `--public`) → correct clone target
  dir + token `.envrc` dir; `cloneFn` seam called with `SSHURL` + target; `--json`
  shape; unknown `--forge` rejected.
- **Python:** `/newrepo foo` → 2×2 keyboard; `newrepo:github:public:foo` callback
  calls `repo_creator("foo","github",False)` and shows Launch button;
  `newrepo_launch:foo` calls `spawner`; empty/invalid name → usage; creator None
  → error reply. Stdlib `unittest`.

## Out of scope (YAGNI)

- GitLab creation (Forgejo + GitHub only for v1).
- Org repos / choosing owner (always `freaxnx01`/`freax`).
- Deleting remote repos (#89).
- A `nav` `Ctrl-N` picker flow (#129) — CLI + bot only here; `nav` can call
  `bridge create` later.
- Auto-launch after create (button only).
