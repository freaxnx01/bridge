## Summary

<!-- 1–3 bullets describing what changed and why. -->

## Cross-shell verification

bridge ships shims for both bash and PowerShell. Confirm both, or explain N/A.

- [ ] Verified under **bash** (Linux/macOS)
- [ ] Verified under **PowerShell** (Windows) — or N/A because: <reason>
- [ ] If shim/launcher/completion changed: `shims/bridge-shim.bats` updated + equivalent pwsh check noted

## Tests

- [ ] `go test ./...` passes
- [ ] `make all` (or `just test`) passes

## Linked issues

<!-- e.g. Closes #65 -->
