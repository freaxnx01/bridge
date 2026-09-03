package herdr

import (
	"fmt"
	"strings"
)

// maxNameLen is Herdr's agent-name limit: [a-z][a-z0-9_-]{0,31}.
const maxNameLen = 32

// agentName derives a legal, unique Herdr agent name from a bridge slot id.
// Herdr requires [a-z][a-z0-9_-]{0,31} and uniqueness among live agents, which
// slot ids do not guarantee: repo names carry uppercase, and a repo plus a
// worktree easily exceeds 32 characters.
//
// taken is the set of live agent names to avoid; on collision the name is
// truncated further and given a "-N" suffix so the total still fits.
func agentName(slot string, taken []string) string {
	base := sanitizeName(slot)
	if !isTaken(base, taken) {
		return base
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("-%d", n)
		trimmed := strings.TrimRight(clip(base, maxNameLen-len(suffix)), "-_")
		candidate := trimmed + suffix
		if !isTaken(candidate, taken) {
			return candidate
		}
	}
}

// sanitizeName lowercases, replaces illegal runes with "-", collapses runs,
// trims separators, guarantees a leading letter, and clips to the limit.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := collapse(b.String())
	out = strings.Trim(out, "-_")
	if out == "" {
		return "agent"
	}
	if first := out[0]; first < 'a' || first > 'z' {
		out = "a-" + out
	}
	return strings.TrimRight(clip(out, maxNameLen), "-_")
}

// collapse squeezes runs of "-" into a single "-".
func collapse(s string) string {
	var b strings.Builder
	var prevDash bool
	for _, r := range s {
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clip(s string, n int) string {
	if n < 1 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func isTaken(name string, taken []string) bool {
	for _, t := range taken {
		if t == name {
			return true
		}
	}
	return false
}
