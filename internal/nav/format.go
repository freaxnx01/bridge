package nav

import (
	"fmt"
	"strings"
	"time"
)

// humanLastAccessed renders d as at most two descending units (d/h/m).
// Sub-minute durations render as "0m".
func humanLastAccessed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// filterRepos keeps rows whose label contains q (case-insensitive). Empty q
// returns all rows. Result is a new slice; input is not mutated.
func filterRepos(rows []repoRow, q string) []repoRow {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	needle := strings.ToLower(q)
	out := make([]repoRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.label), needle) {
			out = append(out, r)
		}
	}
	return out
}
