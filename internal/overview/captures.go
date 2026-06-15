package overview

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

// Config is everything Build needs for one environment. Forge access is
// injected (callbacks) so this package stays client/token-free, matching nav's
// DI idiom.
type Config struct {
	Environment  string      // "Personal" / "Business" (display only)
	Repos        []core.Repo // discovered repos in this environment
	IdeasLabDir  string      // path to ideas-lab idea files; "" disables
	FetchIssues  func(ctx context.Context) ([]Issue, error)
	FetchRoadmap func(ctx context.Context) ([]RankedItem, error) // nil => no board (1a)
	Now          func() time.Time
}

// Issue is the minimal open-issue shape Build needs (decoupled from forge.Issue
// so this package has no forge dependency; cmd/bridge adapts).
type Issue struct {
	Repo    string
	Title   string
	URL     string
	Labels  []string
	Updated time.Time
}

func (cfg Config) now() time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

// collectCaptures reads every raw-capture file source into a flat, recency-
// sorted slice (newest first). Missing files/dirs are skipped silently.
func collectCaptures(cfg Config) []Capture {
	now := cfg.now()
	var out []Capture
	out = append(out, ideasLabCaptures(cfg.IdeasLabDir, now)...)
	for _, r := range cfg.Repos {
		out = append(out, bulletCaptures(filepath.Join(r.Path, "ideas.md"), CaptureRepoIdeas, r.Name, now)...)
		out = append(out, todoCaptures(filepath.Join(r.Path, "TODO.md"), r.Name, now)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Age < out[j].Age })
	return out
}

// ideasLabCaptures treats each *.md file in dir as one capture, titled by its
// first markdown heading or first non-empty line.
func ideasLabCaptures(dir string, now time.Time) []Capture {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Capture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		out = append(out, Capture{
			Source: CaptureIdeasLab,
			Title:  fileTitle(path, e.Name()),
			Path:   path,
			Age:    age(path, now),
		})
	}
	return out
}

// bulletCaptures reads top-level "- "/"* " bullets from a list file.
func bulletCaptures(path string, src CaptureSource, repo string, now time.Time) []Capture {
	lines, ok := readLines(path)
	if !ok {
		return nil
	}
	a := age(path, now)
	var out []Capture
	for _, l := range lines {
		if t, ok := bulletText(l); ok {
			out = append(out, Capture{Source: src, Repo: repo, Title: t, Path: path, Age: a})
		}
	}
	return out
}

// todoCaptures reads only unchecked "- [ ]" lines from a TODO file.
func todoCaptures(path, repo string, now time.Time) []Capture {
	lines, ok := readLines(path)
	if !ok {
		return nil
	}
	a := age(path, now)
	var out []Capture
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "- [ ]") {
			out = append(out, Capture{
				Source: CaptureRepoTodo,
				Repo:   repo,
				Title:  strings.TrimSpace(strings.TrimPrefix(s, "- [ ]")),
				Path:   path,
				Age:    a,
			})
		}
	}
	return out
}

func bulletText(line string) (string, bool) {
	s := strings.TrimSpace(line)
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(s, p) && !strings.HasPrefix(s, "- [") {
			return strings.TrimSpace(strings.TrimPrefix(s, p)), true
		}
	}
	return "", false
}

func fileTitle(path, fallback string) string {
	lines, ok := readLines(path)
	if !ok {
		return fallback
	}
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(s, "# "))
	}
	return fallback
}

func readLines(path string) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	body := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(body, "\n"), true
}

func age(path string, now time.Time) time.Duration {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return now.Sub(fi.ModTime())
}
