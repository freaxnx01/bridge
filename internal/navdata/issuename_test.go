package navdata

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssueWorktreeName(t *testing.T) {
	cases := []struct {
		num       int
		title     string
		wantWT    string
		wantLabel string
	}{
		{127, "show open forge issues", "127-show-open-forge-issues", "#127 [show open forge issues]"},
		{5, "feat(nav): Ctrl+N new repo", "5-feat-nav-ctrl-n-new-repo", "#5 [feat(nav): Ctrl+N new repo]"},
		{42, "", "42", "#42"},
		{9, "!!!", "9", "#9 [!!!]"},
		// Review Focus: emoji and slashes never reach the worktree/branch name.
		{7, "🚀 fix/launch: crash", "7-fix-launch-crash", "#7 [🚀 fix/launch: crash]"},
		{8, "a\nb", "8-a-b", "#8 [a b]"},
	}
	for _, c := range cases {
		wt, label := IssueWorktreeName(c.num, c.title)
		if wt != c.wantWT || label != c.wantLabel {
			t.Errorf("IssueWorktreeName(%d, %q) = %q, %q; want %q, %q", c.num, c.title, wt, label, c.wantWT, c.wantLabel)
		}
	}
}

// Review Focus: a long emoji title must not be cut mid-rune — Telegram rejects
// invalid UTF-8 and tmux/claude would show mojibake.
func TestIssueWorktreeName_LongEmojiTitleStaysValidUTF8(t *testing.T) {
	wt, label := IssueWorktreeName(3, strings.Repeat("🚀", 60))
	if wt != "3" {
		t.Errorf("wt = %q, want %q", wt, "3")
	}
	if !utf8.ValidString(label) {
		t.Fatalf("label is not valid UTF-8: %q", label)
	}
	if n := utf8.RuneCountInString(label); n > len("#3 []")+IssueLabelMax {
		t.Errorf("label has %d runes, want <= %d", n, len("#3 []")+IssueLabelMax)
	}
}

func TestIssueWorktreeName_LongTitleTruncatesSlug(t *testing.T) {
	wt, _ := IssueWorktreeName(1, strings.Repeat("ab cd ", 20))
	if len(wt) > len("1-")+IssueSlugMax || strings.HasSuffix(wt, "-") {
		t.Errorf("slug = %q", wt)
	}
}

func TestTruncRunes(t *testing.T) {
	if got := TruncRunes("héllo wörld", 6); got != "héllo…" {
		t.Errorf("got %q", got)
	}
	if got := TruncRunes("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := TruncRunes("abc", 1); got != "…" {
		t.Errorf("got %q", got)
	}
}
