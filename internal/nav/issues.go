package nav

import "github.com/freaxnx01/bridge/internal/navdata"

// issueSlugMax bounds the title portion of an issue-derived worktree slug.
const issueSlugMax = navdata.IssueSlugMax

// issueWorktreeName derives the worktree basename "<number>-<title-slug>" and
// the session label "#<number> [<short title>]" for an issue (see
// navdata.IssueWorktreeName, shared with `bridge bot`).
func issueWorktreeName(number int, title string) (wt, label string) {
	return navdata.IssueWorktreeName(number, title)
}
