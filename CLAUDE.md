# Commit conventions

- Use **Conventional Commits** format for all commit messages (e.g. `feat: ...`, `fix: ...`).
- When committing any change to `clrepo.sh`, bump `_CLREPO_VERSION` (defined near the top of the file) according to semver: patch for fixes, minor for new features, major for breaking changes.
- Whenever `_CLREPO_VERSION` is bumped, add a matching entry to `CHANGELOG.md` (Keep a Changelog format) in the same commit, with the new version, today's date, and a section (`Added` / `Changed` / `Fixed`) describing the change.
