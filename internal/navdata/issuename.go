package navdata

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// IssueSlugMax bounds the title part of an issue worktree slug.
	IssueSlugMax = 32
	// IssueLabelMax bounds (in runes) the title shown in the session label.
	IssueLabelMax = 40
)

// IssueWorktreeName derives the worktree basename "<number>-<title-slug>" and
// the session label "#<number> [<short title>]" for an issue. The slug is
// ASCII-only (any other rune, including "/" and emoji, is a separator), so it
// is always a safe directory and branch name; the label is truncated on rune
// boundaries, so it is always valid UTF-8. A blank or unslugifiable title
// degrades to just the number ("123" / "#123").
func IssueWorktreeName(number int, title string) (wt, label string) {
	slug := Slugify(title)
	if len(slug) > IssueSlugMax {
		slug = strings.Trim(slug[:IssueSlugMax], "-")
	}
	wt = strconv.Itoa(number)
	if slug != "" {
		wt += "-" + slug
	}
	short := TruncRunes(strings.Join(strings.Fields(title), " "), IssueLabelMax)
	if short == "" {
		return wt, fmt.Sprintf("#%d", number)
	}
	return wt, fmt.Sprintf("#%d [%s]", number, short)
}

// Slugify lowercases s and collapses every run of non-[a-z0-9] runes into one
// hyphen, trimming leading/trailing hyphens.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// TruncRunes shortens s to at most n runes, ending in "…" when cut. It never
// splits a multi-byte rune, so the result is valid UTF-8 whenever s is.
func TruncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
