# Commit conventions

- Use **Conventional Commits** format for all commit messages (e.g. `feat: ...`, `fix: ...`).
- All code lives in `cmd/bridge` (CLI) and `internal/` (libraries). The frozen bash scripts were deleted in Phase 4 (v2.1.0); the Go binary is the only implementation now.
- Tag releases as `vX.Y.Z` via `git tag` and add a `CHANGELOG.md` entry describing the user-visible changes per release. The `v2.0.0-go.N` suffix is retired alongside the bash code.
