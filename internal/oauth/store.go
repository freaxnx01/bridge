// Package oauth implements the OAuth 2.1 authorization-server role bridge
// presents to MCP clients, the OIDC client leg used to delegate the human
// login to authentik, and the token store backing both.
package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenKind distinguishes access tokens from refresh tokens.
type TokenKind string

const (
	KindAccess  TokenKind = "access"
	KindRefresh TokenKind = "refresh"
)

// Client is a dynamically registered OAuth client.
type Client struct {
	RedirectURIs []string  `json:"redirect_uris"`
	Name         string    `json:"client_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

// Code is a pending authorization code, keyed in the store by its hash.
type Code struct {
	ClientID      string    `json:"client_id"`
	RedirectURI   string    `json:"redirect_uri"`
	CodeChallenge string    `json:"code_challenge"`
	Subject       string    `json:"sub"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Token is an issued access or refresh token, keyed in the store by its hash.
type Token struct {
	Kind      TokenKind `json:"kind"`
	ClientID  string    `json:"client_id"`
	Subject   string    `json:"sub"`
	ChainID   string    `json:"chain_id"`
	Consumed  bool      `json:"consumed"`
	ExpiresAt time.Time `json:"expires_at"`
}

type state struct {
	Clients map[string]*Client `json:"clients"`
	Codes   map[string]*Code   `json:"codes"`
	Tokens  map[string]*Token  `json:"tokens"`
}

// Store is the file-backed OAuth state. It is safe for concurrent use; every
// exported method takes mu. Only one process may hold a Store for a given
// directory at a time, enforced by an OS-level lock.
type Store struct {
	mu   sync.Mutex
	path string
	lock *fileLock
	st   state
}

const stateFileName = "oauth.json"

// OpenStore opens (or initialises) the state directory. It fails rather than
// starting with empty state when the file exists but cannot be parsed: silently
// continuing would drop every session and accept fresh registrations as if
// nothing had happened.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod state dir %s: %w", dir, err)
	}

	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}

	s := &Store{
		path: filepath.Join(dir, stateFileName),
		lock: lock,
		st: state{
			Clients: map[string]*Client{},
			Codes:   map[string]*Code{},
			Tokens:  map[string]*Token{},
		},
	}

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		// best-effort unlock; the primary error below is the actionable one
		_ = lock.release()
		return nil, fmt.Errorf("read state %s: %w", s.path, err)
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		// best-effort unlock; the primary error below is the actionable one
		_ = lock.release()
		return nil, fmt.Errorf("parse state %s: %w", s.path, err)
	}
	if s.st.Clients == nil {
		s.st.Clients = map[string]*Client{}
	}
	if s.st.Codes == nil {
		s.st.Codes = map[string]*Code{}
	}
	if s.st.Tokens == nil {
		s.st.Tokens = map[string]*Token{}
	}
	return s, nil
}

// Close releases the single-instance lock.
func (s *Store) Close() error { return s.lock.release() }

// save writes the state atomically. The caller must hold s.mu.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".oauth-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		// best-effort close; the primary error below is the actionable one
		_ = tmp.Close()
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		// best-effort close; the primary error below is the actionable one
		_ = tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		// best-effort close; the primary error below is the actionable one
		_ = tmp.Close()
		return fmt.Errorf("sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("rename state into place: %w", err)
	}
	return nil
}
