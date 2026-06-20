// Package capture writes captured ideas to a Git-backed destination via an
// injected FileWriter (the GitHub Contents API in production). It is
// forge-token-free: callers supply the writer and the resolved repo/token.
package capture

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Target is where a captured idea lands: the ideas-lab repo (a new dated file)
// or a specific repo (appended to its ideas.md).
type Target struct {
	IdeasLab bool
	Owner    string
	Repo     string
}

// FileWriter is the subset of the forge client capture needs (consumer iface).
type FileWriter interface {
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
	PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (htmlURL string, err error)
}

// CaptureIdea writes text to the target and returns the file's URL.
func CaptureIdea(ctx context.Context, w FileWriter, t Target, text string, now time.Time) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty idea text")
	}
	if t.IdeasLab {
		path := fmt.Sprintf("ideas/%s-%s.md", now.Format("2006-01-02"), slug(text))
		body := fmt.Sprintf("Status: seed\nCaptured: %s (Telegram capture)\n\n%s\n", now.Format("2006-01-02"), text)
		return w.PutFile(ctx, t.Owner, t.Repo, path, []byte(body), "capture: "+slug(text), "")
	}
	const path = "ideas.md"
	existing, sha, found, err := w.GetFile(ctx, t.Owner, t.Repo, path)
	if err != nil {
		return "", err
	}
	var content string
	if !found || len(existing) == 0 {
		content = "# Ideas\n\n- " + text + "\n"
		sha = ""
	} else {
		content = string(existing)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "- " + text + "\n"
	}
	return w.PutFile(ctx, t.Owner, t.Repo, path, []byte(content), "capture: idea", sha)
}

// slug turns idea text into a filename-safe slug (lowercase, non-alnum -> "-",
// collapsed, trimmed, <=50 chars). Empty/punctuation-only -> "idea".
func slug(text string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	if s == "" {
		return "idea"
	}
	return s
}
