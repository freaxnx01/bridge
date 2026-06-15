package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestCollectCaptures_AllSources(t *testing.T) {
	root := t.TempDir()
	// ideas-lab: one file per idea
	ideasLab := filepath.Join(root, "ideas-lab")
	mustWrite(t, filepath.Join(ideasLab, "2026-06-01-kanban.md"), "# Kanban for issues\n\nrough\n")
	// a repo with ideas.md + TODO.md (bullets)
	repoPath := filepath.Join(root, "bridge")
	mustWrite(t, filepath.Join(repoPath, "ideas.md"), "# Ideas\n\n- multi-pane focus model\n- weighted overview\n")
	mustWrite(t, filepath.Join(repoPath, "TODO.md"), "- [ ] wire api skeleton\n- [x] done thing\n- [ ] add tests\n")

	cfg := Config{
		IdeasLabDir: ideasLab,
		Repos:       []core.Repo{{Name: "bridge", Path: repoPath}},
		Now:         func() time.Time { return time.Now() },
	}
	got := collectCaptures(cfg)

	countBy := map[CaptureSource]int{}
	for _, c := range got {
		countBy[c.Source]++
	}
	if countBy[CaptureIdeasLab] != 1 {
		t.Errorf("ideas-lab captures = %d, want 1", countBy[CaptureIdeasLab])
	}
	if countBy[CaptureRepoIdeas] != 2 {
		t.Errorf("ideas.md captures = %d, want 2", countBy[CaptureRepoIdeas])
	}
	if countBy[CaptureRepoTodo] != 2 { // only the two "- [ ]" lines, not "- [x]"
		t.Errorf("TODO.md captures = %d, want 2", countBy[CaptureRepoTodo])
	}
	// ideas-lab title comes from the first heading/line
	for _, c := range got {
		if c.Source == CaptureIdeasLab && c.Title != "Kanban for issues" {
			t.Errorf("ideas-lab title = %q, want %q", c.Title, "Kanban for issues")
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
