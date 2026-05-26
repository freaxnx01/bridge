# Commit conventions

- Use **Conventional Commits** format for all commit messages (e.g. `feat: ...`, `fix: ...`).
- `bridge.sh` and the other bash scripts (`bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`) are frozen as of Phase 3 cutover (v2.0.0). Do not edit them — fixes land in the Go binary (`cmd/bridge`). The `_BRIDGE_VERSION` rule is retired.
- For Go changes, bump the `v2.0.0-go.N` series via `git tag` when shipping; `CHANGELOG.md` entries describe the user-visible changes per release.
