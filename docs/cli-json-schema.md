# `bridge --json` schemas (Plan A)

All read commands accept `--json`. Output goes to stdout. Errors go to stderr as a single line: `{"error":"...","code":N}`.

## `bridge list --json`

Without `-r`: array of `Repo`:

```json
[
  {
    "name": "bridge",
    "path": "/home/me/projects/repos/github/me/public/bridge",
    "forge": "github",
    "owner": "me",
    "visibility": "public",
    "topics": [],
    "desc": "",
    "default_branch": "",
    "remote_url": "",
    "last_used": "0001-01-01T00:00:00Z"
  }
]
```

With `-r`:

```json
{
  "local": [ /* Repo[] as above */ ],
  "remote": [
    {
      "forge": "github",
      "owner": "me",
      "name": "bridge",
      "default_branch": "main",
      "description": "",
      "topics": [],
      "visibility": "public",
      "html_url": "https://github.com/me/bridge",
      "ssh_url": "git@github.com:me/bridge.git",
      "updated_at": "2026-05-01T00:00:00Z"
    }
  ]
}
```

## `bridge slots --json`

Array of `Slot`:

```json
[
  {
    "id": "bridge-main",
    "repo": "bridge",
    "worktree": "",
    "agent": "claude",
    "created": "2026-05-01T00:00:00Z"
  }
]
```

## `bridge sessions --json`

Array of `Session`:

```json
[
  {
    "slot_id": "bridge-main",
    "state": "attached",
    "age": 7200000000000,
    "pid": 0,
    "tmux_name": "bridge-main"
  }
]
```

`age` is `time.Duration` nanoseconds (Go default JSON encoding).

## `bridge presence --json`

```json
{
  "mode": "away",
  "overrides": {"slot-id": "on"},
  "updated_at": "2026-05-01T00:00:00Z"
}
```

## `bridge sync --json`

```json
{
  "last_run": "2026-05-01T00:00:00Z",
  "queue": ["repo-a", "repo-b"],
  "unpushed": ["owner/repo"]
}
```

## `bridge issues --json`

Array of `Issue`:

```json
[
  {
    "forge": "github",
    "repo": "me/bridge",
    "number": 30,
    "title": "feat: dashboard",
    "url": "https://...",
    "labels": ["area:tui"],
    "updated": "2026-05-01T00:00:00Z"
  }
]
```

## `bridge status --json`

```json
{
  "sessions": 0,
  "presence": "auto",
  "sync": {"unpushed": 0},
  "version": "bridge dev (commit none, built unknown)"
}
```

## Error shape

```json
{"error": "repo not found", "code": 2}
```

Codes: `1` internal/FS/subprocess; `2` user input; `3` network with no cache fallback.
