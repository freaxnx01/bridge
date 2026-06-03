package nav

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// issueSlugMax bounds the title portion of an issue-derived worktree slug so
	// the directory/branch name stays short and filesystem-safe.
	issueSlugMax = 32
	// issueLabelMax bounds the title shown in the session display label.
	issueLabelMax = 40
)

// issueWorktreeName derives a filesystem-safe worktree basename and the session
// display label for an issue. The basename is "<number>-<title-slug>" (e.g.
// "123-show-open-forge-issues"); the label is "#123 [<short title>]". A blank or
// unslugifiable title degrades to just the number ("123" / "#123").
func issueWorktreeName(number int, title string) (wt, label string) {
	slug := slugify(title)
	if len(slug) > issueSlugMax {
		slug = strings.Trim(slug[:issueSlugMax], "-")
	}
	wt = strconv.Itoa(number)
	if slug != "" {
		wt += "-" + slug
	}
	short := trunc(strings.TrimSpace(title), issueLabelMax)
	if short == "" {
		return wt, fmt.Sprintf("#%d", number)
	}
	return wt, fmt.Sprintf("#%d [%s]", number, short)
}

// slugify lowercases s and collapses every run of non-alphanumeric characters
// into a single hyphen, trimming leading/trailing hyphens. ASCII-only: any
// non-[a-z0-9] rune (including accented letters) becomes a separator.
func slugify(s string) string {
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
