package oauth

import (
	"fmt"
	"testing"
	"time"
)

func TestPrune_RemovesOnlyExpiredRecords(t *testing.T) {
	now := time.Now()
	s := &Store{st: state{
		Clients: map[string]*Client{},
		Codes: map[string]*Code{
			"live": {ExpiresAt: now.Add(time.Minute)},
			"dead": {ExpiresAt: now.Add(-time.Second)},
		},
		Tokens: map[string]*Token{
			"live": {ExpiresAt: now.Add(time.Hour)},
			"dead": {ExpiresAt: now.Add(-time.Hour)},
		},
	}}

	s.prune(now)

	if _, ok := s.st.Codes["dead"]; ok {
		t.Error("expired code survived prune")
	}
	if _, ok := s.st.Codes["live"]; !ok {
		t.Error("live code was pruned")
	}
	if _, ok := s.st.Tokens["dead"]; ok {
		t.Error("expired token survived prune")
	}
	if _, ok := s.st.Tokens["live"]; !ok {
		t.Error("live token was pruned")
	}
}

func TestEnforceClientCap_EvictsOldestUnusedBeyondCap(t *testing.T) {
	now := time.Now()
	s := &Store{st: state{Clients: map[string]*Client{}, Codes: map[string]*Code{}, Tokens: map[string]*Token{}}}

	// maxClients+5 registrations, each used, oldest first.
	for i := 0; i < maxClients+5; i++ {
		s.st.Clients[fmt.Sprintf("c%03d", i)] = &Client{
			CreatedAt:  now.Add(-time.Duration(maxClients+5-i) * time.Minute),
			LastUsedAt: now.Add(-time.Duration(maxClients+5-i) * time.Minute),
		}
	}

	s.enforceClientCap(now)

	if len(s.st.Clients) != maxClients {
		t.Fatalf("client count = %d, want %d", len(s.st.Clients), maxClients)
	}
	if _, ok := s.st.Clients["c000"]; ok {
		t.Error("oldest client survived eviction")
	}
	if _, ok := s.st.Clients[fmt.Sprintf("c%03d", maxClients+4)]; !ok {
		t.Error("newest client was evicted")
	}
}

func TestEnforceClientCap_DropsRegistrationsNeverUsed(t *testing.T) {
	now := time.Now()
	s := &Store{st: state{Clients: map[string]*Client{
		"stale": {CreatedAt: now.Add(-unusedClientTTL - time.Minute)}, // zero LastUsedAt
		"fresh": {CreatedAt: now.Add(-time.Minute)},
		"used":  {CreatedAt: now.Add(-unusedClientTTL - time.Hour), LastUsedAt: now.Add(-time.Minute)},
	}, Codes: map[string]*Code{}, Tokens: map[string]*Token{}}}

	s.enforceClientCap(now)

	if _, ok := s.st.Clients["stale"]; ok {
		t.Error("registration never used and past TTL survived")
	}
	if _, ok := s.st.Clients["fresh"]; !ok {
		t.Error("recent unused registration was dropped too eagerly")
	}
	if _, ok := s.st.Clients["used"]; !ok {
		t.Error("old but used registration was dropped")
	}
}
