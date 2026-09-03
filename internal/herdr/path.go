package herdr

import (
	"path/filepath"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// worktreeDir is the fixed directory bridge creates worktrees in (CLAUDE.md).
const worktreeDir = ".worktrees"

// SlotIDForPath maps an agent's working directory to the bridge slot id that
// would have launched it, inverting bridge's own layout:
//
//	/…/<repo>                   -> "<repo>"
//	/…/<repo>/.worktrees/<wt>   -> "<repo>-wt-<wt>"
//
// It is a pure function of the path — it never touches the filesystem — so a
// directory deeper inside a repo maps to that directory's own basename and
// simply matches no dashboard row. Returns "" for a path with no usable
// basename.
func SlotIDForPath(cwd string) string {
	clean := filepath.Clean(strings.TrimSpace(cwd))
	if clean == "" || clean == "." || clean == string(filepath.Separator) {
		return ""
	}
	base := filepath.Base(clean)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) == worktreeDir {
		return core.SlotID(filepath.Base(filepath.Dir(parent)), base)
	}
	if base == worktreeDir {
		return core.SlotID(filepath.Base(parent), "")
	}
	return core.SlotID(base, "")
}
