package capture

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeWriter records PutFile calls and serves canned GetFile responses.
type fakeWriter struct {
	files map[string]struct {
		content []byte
		sha     string
	}
	puts []struct {
		owner, repo, path, message, sha string
		content                         []byte
	}
}

func (f *fakeWriter) GetFile(_ context.Context, owner, repo, path string) ([]byte, string, bool, error) {
	if f.files == nil {
		return nil, "", false, nil
	}
	v, ok := f.files[owner+"/"+repo+"/"+path]
	if !ok {
		return nil, "", false, nil
	}
	return v.content, v.sha, true, nil
}

func (f *fakeWriter) PutFile(_ context.Context, owner, repo, path string, content []byte, message, sha string) (string, error) {
	f.puts = append(f.puts, struct {
		owner, repo, path, message, sha string
		content                         []byte
	}{owner, repo, path, message, sha, content})
	return "https://example/" + path, nil
}

var fixedNow = time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)

func TestCaptureIdea_IdeasLab_NewDatedFile(t *testing.T) {
	w := &fakeWriter{}
	_, err := CaptureIdea(context.Background(), w, Target{IdeasLab: true, Owner: "freaxnx01", Repo: "ideas-lab"}, "Kanban for issues!", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.path != "ideas/2026-06-16-kanban-for-issues.md" {
		t.Errorf("path = %q", p.path)
	}
	if p.sha != "" {
		t.Errorf("ideas-lab create must send empty sha, got %q", p.sha)
	}
	body := string(p.content)
	if !strings.Contains(body, "Status: seed") || !strings.Contains(body, "Captured: 2026-06-16 (Telegram capture)") || !strings.Contains(body, "Kanban for issues!") {
		t.Errorf("body missing preamble/text:\n%s", body)
	}
}

func TestCaptureIdea_Repo_AppendsToExistingIdeas(t *testing.T) {
	w := &fakeWriter{files: map[string]struct {
		content []byte
		sha     string
	}{
		"freaxnx01/bridge/ideas.md": {content: []byte("# Ideas\n\n- one\n"), sha: "s1"},
	}}
	_, err := CaptureIdea(context.Background(), w, Target{Owner: "freaxnx01", Repo: "bridge"}, "two", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.path != "ideas.md" || p.sha != "s1" {
		t.Errorf("path=%q sha=%q", p.path, p.sha)
	}
	if string(p.content) != "# Ideas\n\n- one\n- two\n" {
		t.Errorf("append wrong:\n%q", string(p.content))
	}
}

func TestCaptureIdea_Repo_AppendsWhenNoTrailingNewline(t *testing.T) {
	w := &fakeWriter{files: map[string]struct {
		content []byte
		sha     string
	}{
		"freaxnx01/bridge/ideas.md": {content: []byte("# Ideas\n\n- one"), sha: "s9"}, // NO trailing newline
	}}
	_, err := CaptureIdea(context.Background(), w, Target{Owner: "freaxnx01", Repo: "bridge"}, "two", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(w.puts[0].content); got != "# Ideas\n\n- one\n- two\n" {
		t.Errorf("append-without-trailing-newline wrong:\n%q", got)
	}
}

func TestCaptureIdea_Repo_CreatesWhenAbsent(t *testing.T) {
	w := &fakeWriter{}
	_, err := CaptureIdea(context.Background(), w, Target{Owner: "freaxnx01", Repo: "bridge"}, "first", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.sha != "" || string(p.content) != "# Ideas\n\n- first\n" {
		t.Errorf("create wrong: sha=%q content=%q", p.sha, string(p.content))
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Kanban for issues!", "kanban-for-issues"},
		{"  multiple   spaces  ", "multiple-spaces"},
		{"", "idea"},
		{"!!!", "idea"},
	}
	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
