package herdr

import "testing"

func TestSlotIDForPath_MapsBridgeLayoutToSlotIDs(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"repo root", "/home/a/repos/github/freaxnx01/public/bridge", "bridge"},
		{"worktree", "/home/a/repos/github/freaxnx01/public/bridge/.worktrees/foo", "bridge-wt-foo"},
		{"trailing slash on repo root", "/home/a/repos/x/bridge/", "bridge"},
		{"trailing slash on worktree", "/home/a/repos/x/bridge/.worktrees/foo/", "bridge-wt-foo"},
		{"uppercase repo name is preserved", "/home/a/repos/x/BI_ExportSQLiteToCsv", "BI_ExportSQLiteToCsv"},
		{"nested dir inside a repo is not the repo", "/home/a/repos/x/bridge/internal/nav", "nav"},
		{"empty", "", ""},
		{"root", "/", ""},
		{"empty worktree name falls back to the repo", "/home/a/repos/x/bridge/.worktrees", "bridge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SlotIDForPath(tt.cwd); got != tt.want {
				t.Errorf("SlotIDForPath(%q) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}
