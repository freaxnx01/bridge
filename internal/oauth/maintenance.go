package oauth

import (
	"sort"
	"time"
)

const (
	// maxClients bounds the registration table. The DCR endpoint is
	// unauthenticated by spec and internet-facing, so without a cap anyone
	// could grow the state file without limit — a disk-fill denial of service.
	maxClients = 100
	// unusedClientTTL drops registrations that never completed a flow.
	unusedClientTTL = 24 * time.Hour
)

// prune deletes expired codes and tokens. The caller must hold s.mu.
func (s *Store) prune(now time.Time) {
	for k, c := range s.st.Codes {
		if !c.ExpiresAt.After(now) {
			delete(s.st.Codes, k)
		}
	}
	for k, tok := range s.st.Tokens {
		if !tok.ExpiresAt.After(now) {
			delete(s.st.Tokens, k)
		}
	}
}

// revokeChain deletes every token sharing chainID. The caller must hold s.mu.
func (s *Store) revokeChain(chainID string) {
	for k, tok := range s.st.Tokens {
		if tok.ChainID == chainID {
			delete(s.st.Tokens, k)
		}
	}
}

// enforceClientCap drops never-used registrations past their TTL, then evicts
// the oldest-used clients until the table is within maxClients. The caller
// must hold s.mu.
func (s *Store) enforceClientCap(now time.Time) {
	for id, c := range s.st.Clients {
		if c.LastUsedAt.IsZero() && now.Sub(c.CreatedAt) > unusedClientTTL {
			delete(s.st.Clients, id)
		}
	}
	if len(s.st.Clients) <= maxClients {
		return
	}

	type entry struct {
		id   string
		seen time.Time
	}
	entries := make([]entry, 0, len(s.st.Clients))
	for id, c := range s.st.Clients {
		seen := c.LastUsedAt
		if seen.IsZero() {
			seen = c.CreatedAt
		}
		entries = append(entries, entry{id: id, seen: seen})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seen.Before(entries[j].seen) })

	for i := 0; len(s.st.Clients) > maxClients && i < len(entries); i++ {
		delete(s.st.Clients, entries[i].id)
	}
}
