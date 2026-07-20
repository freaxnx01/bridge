# MCP OAuth Resource Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Claude custom connectors (web/mobile) reach `bridge mcp serve` by making bridge an OAuth 2.1 authorization server that delegates the human login to authentik.

**Architecture:** A new `internal/oauth` package holds a file-backed store, the OAuth endpoints, and an `auth.TokenVerifier`. Bridge is the authorization server toward Claude (Dynamic Client Registration + PKCE + public clients — none of which authentik supports) and a confidential OIDC client toward authentik for the actual login. Tokens are opaque random strings stored SHA-256 hashed, so there is no JWT dependency, no signing key, and no cryptographic verification code anywhere.

**Tech Stack:** Go 1.25 stdlib, `github.com/modelcontextprotocol/go-sdk` v1.6.1 (`auth` and `oauthex` packages — already a dependency). No new modules.

**Spec:** `docs/superpowers/specs/2026-07-20-mcp-oauth-resource-server-design.md`

## Global Constraints

- **No new Go modules.** Everything uses the stdlib plus the already-present `go-sdk`. Do not add a JWT, OAuth, assertion, or mocking library.
- **Tests:** stdlib `testing` only, table-driven with `t.Run` subtests, hand-rolled fakes. No testify/mockery/gomock.
- **All tests must pass under `go test -race ./...`**, not just the new ones.
- **Never log** tokens, authorization codes, PKCE verifiers, or the authentik client secret — at any level. Log auth decisions as `sub` + `client_id` + outcome only.
- **Never `panic`** outside genuinely unreachable programmer errors; return wrapped errors with `%w`.
- **No package-level mutable state.** Dependencies are passed via constructors.
- **`--auth=static` must remain the default and behave byte-for-byte as it does today.** Existing `StaticBearerVerifier` tests must pass untouched.
- **Cross-compilation must keep working** (`GOOS=windows go build ./...`). Anything using `syscall.Flock` needs build tags.
- Access tokens live **1 hour**; refresh tokens **30 days**; authorization codes **60 seconds**.
- Registered-client cap: **100**, evicting the oldest unused; registrations never used to complete a flow expire after **24 hours**.
- After every task: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/oauth/store.go` | State types, load/save, atomic write, permissions |
| `internal/oauth/store_test.go` | Store round-trip, permissions, atomicity, concurrency |
| `internal/oauth/maintenance.go` | Pruning, client cap/eviction, registration TTL |
| `internal/oauth/maintenance_test.go` | Cap, eviction, TTL, prune behaviour |
| `internal/oauth/lock_unix.go` | Single-instance lock (`//go:build unix`) |
| `internal/oauth/lock_windows.go` | Single-instance lock (`//go:build windows`) |
| `internal/oauth/token.go` | Token minting, hashing, `Verifier()` |
| `internal/oauth/token_test.go` | Verifier accept/reject cases |
| `internal/oauth/config.go` | Config struct + fail-fast validation |
| `internal/oauth/config_test.go` | Missing-value reporting |
| `internal/oauth/metadata.go` | AS metadata document |
| `internal/oauth/metadata_test.go` | Advertised capabilities |
| `internal/oauth/register.go` | `POST /oauth/register` (DCR) |
| `internal/oauth/register_test.go` | Registration + cap enforcement |
| `internal/oauth/authorize.go` | `GET /oauth/authorize`, redirect_uri matching, PKCE checks |
| `internal/oauth/authorize_test.go` | redirect_uri exact-match table, PKCE table |
| `internal/oauth/authentik.go` | OIDC client leg: discovery, code exchange, userinfo |
| `internal/oauth/authentik_test.go` | Uses the fake authentik |
| `internal/oauth/callback.go` | `GET /oauth/callback`, `sub` check, code issuance |
| `internal/oauth/callback_test.go` | sub mismatch, replayed/unknown state |
| `internal/oauth/token_endpoint.go` | `POST /oauth/token`: code + refresh grants |
| `internal/oauth/token_endpoint_test.go` | PKCE verify, single-use, rotation, reuse detection |
| `internal/oauth/server.go` | `Server` type, route mux for the OAuth subtree |
| `internal/oauth/fake_authentik_test.go` | Shared fake authentik `httptest.Server` |
| `internal/oauth/integration_test.go` | Full end-to-end flow |
| `cmd/bridge/mcp.go` | `--auth` flag, mux assembly, middleware placement |
| `cmd/bridge/mcp_test.go` | Handler routing + `--auth=static` regression |

---

## Task 1: Store foundations

**Files:**
- Create: `internal/oauth/store.go`
- Test: `internal/oauth/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Client`, `type Code`, `type Token`, `type TokenKind` with constants `KindAccess`/`KindRefresh`, `type Store`, `func OpenStore(dir string) (*Store, error)`, `func (*Store) Close() error`, and internal `func (*Store) save() error` (callers hold `s.mu`).

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStore_RoundTrip_PersistsRecords(t *testing.T) {
	dir := t.TempDir()

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	s.mu.Lock()
	s.st.Clients["cid"] = &Client{
		RedirectURIs: []string{"https://claude.ai/cb"},
		Name:         "Claude",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, ok := reopened.st.Clients["cid"]
	if !ok {
		t.Fatalf("client not persisted; state = %+v", reopened.st)
	}
	if len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != "https://claude.ai/cb" {
		t.Errorf("redirect_uris = %v, want [https://claude.ai/cb]", got.RedirectURIs)
	}
}

func TestOpenStore_Permissions_DirAndFileAreRestrictive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	s.mu.Lock()
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.mu.Unlock()

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}

	fi, err := os.Stat(filepath.Join(dir, "oauth.json"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}

func TestOpenStore_CorruptFile_RefusesToStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "oauth.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenStore(dir)
	if err == nil {
		s.Close()
		t.Fatal("want error on corrupt state file, got nil (would silently drop all sessions)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestOpenStore -v`
Expected: FAIL — `undefined: OpenStore`.

- [ ] **Step 3: Write minimal implementation**

```go
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
		lock.release()
		return nil, fmt.Errorf("read state %s: %w", s.path, err)
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		lock.release()
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
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestOpenStore -race -v`
Expected: PASS — all three subtests. (Requires Task 3's `acquireLock`; implement Task 3 first if the package does not compile. See note below.)

> **Ordering note:** `OpenStore` references `acquireLock`/`fileLock` from Task 3. If you are executing strictly in order, add this temporary stub to `store.go` to keep the package compiling, and delete it in Task 3:
> ```go
> type fileLock struct{}
> func acquireLock(string) (*fileLock, error) { return &fileLock{}, nil }
> func (*fileLock) release() error            { return nil }
> ```

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/store.go internal/oauth/store_test.go
git commit -m "feat(oauth): file-backed state store with atomic writes"
```

---

## Task 2: Single-instance lock

**Files:**
- Create: `internal/oauth/lock_unix.go`, `internal/oauth/lock_windows.go`
- Modify: `internal/oauth/store.go` (delete the Task 1 stub)
- Test: `internal/oauth/lock_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type fileLock`, `func acquireLock(dir string) (*fileLock, error)`, `func (*fileLock) release() error`.

Two implementations because `syscall.Flock` does not exist on Windows and bridge cross-compiles there.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import "testing"

func TestOpenStore_SecondInstance_IsRejected(t *testing.T) {
	dir := t.TempDir()

	first, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("first OpenStore: %v", err)
	}
	defer first.Close()

	second, err := OpenStore(dir)
	if err == nil {
		second.Close()
		t.Fatal("second OpenStore succeeded; concurrent instances would clobber state")
	}

	// Releasing the first must let a new instance in.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	third, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore after release: %v", err)
	}
	third.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestOpenStore_SecondInstance -v`
Expected: FAIL — "second OpenStore succeeded" (the Task 1 stub always succeeds).

- [ ] **Step 3: Write minimal implementation**

Delete the stub from `store.go`, then create `internal/oauth/lock_unix.go`:

```go
//go:build unix

package oauth

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock is an advisory OS lock ensuring only one bridge process uses a
// given state directory. Concurrent writers would silently clobber state.
type fileLock struct{ f *os.File }

func acquireLock(dir string) (*fileLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another bridge instance is using state dir %s: %w", dir, err)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		l.f.Close()
		return fmt.Errorf("unlock: %w", err)
	}
	return l.f.Close()
}
```

Create `internal/oauth/lock_windows.go`:

```go
//go:build windows

package oauth

import (
	"fmt"
	"os"
	"path/filepath"
)

// fileLock uses exclusive file creation on Windows, where flock(2) has no
// equivalent. A stale lock after a crash must be removed by hand; that is
// acceptable because the MCP server is deployed on Linux and this build
// exists so cross-compilation keeps working.
type fileLock struct{ path string }

func acquireLock(dir string) (*fileLock, error) {
	path := filepath.Join(dir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("another bridge instance is using state dir %s (or a stale %s remains): %w", dir, path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close lock file: %w", err)
	}
	return &fileLock{path: path}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.path == "" {
		return nil
	}
	return os.Remove(l.path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -race -v && GOOS=windows go build ./...`
Expected: PASS, and the Windows build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/lock_unix.go internal/oauth/lock_windows.go internal/oauth/lock_test.go internal/oauth/store.go
git commit -m "feat(oauth): single-instance lock on the state directory"
```

---

## Task 3: Pruning, client cap, and registration TTL

**Files:**
- Create: `internal/oauth/maintenance.go`
- Test: `internal/oauth/maintenance_test.go`

**Interfaces:**
- Consumes: `Store`, `Client`, `Code`, `Token` from Task 1.
- Produces: `func (*Store) prune(now time.Time)`, `func (*Store) enforceClientCap(now time.Time)`, constants `maxClients = 100`, `unusedClientTTL = 24 * time.Hour`. Both assume the caller holds `s.mu`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run 'TestPrune|TestEnforceClientCap' -v`
Expected: FAIL — `undefined: prune`, `undefined: maxClients`.

- [ ] **Step 3: Write minimal implementation**

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run 'TestPrune|TestEnforceClientCap' -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/maintenance.go internal/oauth/maintenance_test.go
git commit -m "feat(oauth): prune expired records and bound the client table"
```

---

## Task 4: Token minting, hashing, and the verifier

**Files:**
- Create: `internal/oauth/token.go`
- Test: `internal/oauth/token_test.go`

**Interfaces:**
- Consumes: `Store`, `Token`, `KindAccess`, `KindRefresh`, `prune`.
- Produces:
  - `func newSecret() (string, error)` — 32 random bytes, base64url, no padding.
  - `func hashSecret(s string) string` — hex SHA-256.
  - `func (*Store) IssueTokenPair(clientID, subject, chainID string, now time.Time) (access, refresh string, err error)`
  - `func (*Store) Verifier() auth.TokenVerifier`
  - constants `accessTokenTTL = time.Hour`, `refreshTokenTTL = 30 * 24 * time.Hour`.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestVerifier_AcceptsIssuedAccessTokenOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now()
	access, refresh, err := s.IssueTokenPair("cid", "sub-123", "chain-1", now)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	verify := s.Verifier()

	info, err := verify(context.Background(), access, nil)
	if err != nil {
		t.Fatalf("valid access token rejected: %v", err)
	}
	if info.UserID != "sub-123" {
		t.Errorf("UserID = %q, want sub-123 (needed for session binding)", info.UserID)
	}
	if !info.Expiration.After(now) {
		t.Errorf("Expiration %v not in the future", info.Expiration)
	}

	// A refresh token must not authenticate MCP calls.
	if _, err := verify(context.Background(), refresh, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("refresh token accepted at the resource server: %v", err)
	}
}

func TestVerifier_RejectsUnknownExpiredAndRevoked(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	valid, _, err := s.IssueTokenPair("cid", "sub-123", "chain-1", time.Now())
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	expired, _, err := s.IssueTokenPair("cid", "sub-123", "chain-2", time.Now().Add(-2*accessTokenTTL))
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	revoked := valid
	s.mu.Lock()
	delete(s.st.Tokens, hashSecret(revoked))
	s.mu.Unlock()

	verify := s.Verifier()
	tests := []struct {
		name  string
		token string
	}{
		{"unknown", "not-a-real-token"},
		{"empty", ""},
		{"expired", expired},
		{"revoked", revoked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := verify(context.Background(), tt.token, nil); !errors.Is(err, auth.ErrInvalidToken) {
				t.Errorf("token %q: want ErrInvalidToken, got %v", tt.name, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestVerifier -v`
Expected: FAIL — `undefined: IssueTokenPair`.

- [ ] **Step 3: Write minimal implementation**

```go
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// newSecret returns a 256-bit random secret, base64url encoded without padding.
// Used for access tokens, refresh tokens, authorization codes, and client IDs.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret returns the hex SHA-256 of s. Only hashes are persisted, so a
// leaked state file yields no usable credentials.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// IssueTokenPair mints an access/refresh pair belonging to one rotation chain.
func (s *Store) IssueTokenPair(clientID, subject, chainID string, now time.Time) (string, string, error) {
	access, err := newSecret()
	if err != nil {
		return "", "", err
	}
	refresh, err := newSecret()
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.st.Tokens[hashSecret(access)] = &Token{
		Kind: KindAccess, ClientID: clientID, Subject: subject,
		ChainID: chainID, ExpiresAt: now.Add(accessTokenTTL),
	}
	s.st.Tokens[hashSecret(refresh)] = &Token{
		Kind: KindRefresh, ClientID: clientID, Subject: subject,
		ChainID: chainID, ExpiresAt: now.Add(refreshTokenTTL),
	}
	s.prune(now)
	if err := s.save(); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Verifier returns the auth.TokenVerifier guarding the MCP transport. It
// mirrors StaticBearerVerifier's shape so the transport wiring is unchanged.
// Only access tokens authenticate MCP calls; a refresh token presented here is
// rejected.
func (s *Store) Verifier() auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token == "" {
			return nil, auth.ErrInvalidToken
		}
		s.mu.Lock()
		defer s.mu.Unlock()

		rec, ok := s.st.Tokens[hashSecret(token)]
		if !ok || rec.Kind != KindAccess || !rec.ExpiresAt.After(time.Now()) {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			UserID:     rec.Subject,
			Expiration: rec.ExpiresAt,
		}, nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestVerifier -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/token.go internal/oauth/token_test.go
git commit -m "feat(oauth): opaque token issuance and resource-server verifier"
```

---

## Task 5: Config and fail-fast validation

**Files:**
- Create: `internal/oauth/config.go`
- Test: `internal/oauth/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Config struct { Issuer, AuthentikIssuer, ClientID, ClientSecret, AllowedSubject, StateDir string }` and `func (Config) Validate() error`.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Issuer:          "https://bridge-mcp.example.com",
		AuthentikIssuer: "https://auth.example.com/application/o/bridge/",
		ClientID:        "cid",
		ClientSecret:    "secret",
		AllowedSubject:  "sub-123",
		StateDir:        "/tmp/state",
	}
}

func TestConfigValidate_ReportsEveryMissingValueAtOnce(t *testing.T) {
	err := Config{StateDir: "/tmp/state"}.Validate()
	if err == nil {
		t.Fatal("want error for empty config")
	}
	for _, want := range []string{
		"BRIDGE_MCP_ISSUER", "BRIDGE_OIDC_ISSUER",
		"BRIDGE_OIDC_CLIENT_ID", "BRIDGE_OIDC_CLIENT_SECRET", "BRIDGE_OIDC_ALLOWED_SUB",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestConfigValidate_IssuerScheme(t *testing.T) {
	tests := []struct {
		name    string
		issuer  string
		wantErr bool
	}{
		{"https", "https://bridge-mcp.example.com", false},
		{"loopback http allowed for dev", "http://127.0.0.1:7788", false},
		{"localhost http allowed for dev", "http://localhost:7788", false},
		{"plain http rejected", "http://bridge-mcp.example.com", true},
		{"not a url", "://nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			c.Issuer = tt.issuer
			err := c.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("issuer %q: want error, got nil", tt.issuer)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("issuer %q: unexpected error %v", tt.issuer, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestConfig -v`
Expected: FAIL — `undefined: Config`.

- [ ] **Step 3: Write minimal implementation**

```go
package oauth

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Config is the OAuth-mode configuration, resolved from the environment by
// cmd/bridge. Every field is required.
type Config struct {
	Issuer          string // BRIDGE_MCP_ISSUER — bridge's public base URL
	AuthentikIssuer string // BRIDGE_OIDC_ISSUER
	ClientID        string // BRIDGE_OIDC_CLIENT_ID
	ClientSecret    string // BRIDGE_OIDC_CLIENT_SECRET
	AllowedSubject  string // BRIDGE_OIDC_ALLOWED_SUB — the only permitted sub
	StateDir        string // BRIDGE_MCP_STATE_DIR
}

// Validate reports every problem at once, so a misconfigured deployment is
// fixed in one pass rather than one restart per missing variable.
func (c Config) Validate() error {
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"BRIDGE_MCP_ISSUER", c.Issuer},
		{"BRIDGE_OIDC_ISSUER", c.AuthentikIssuer},
		{"BRIDGE_OIDC_CLIENT_ID", c.ClientID},
		{"BRIDGE_OIDC_CLIENT_SECRET", c.ClientSecret},
		{"BRIDGE_OIDC_ALLOWED_SUB", c.AllowedSubject},
		{"BRIDGE_MCP_STATE_DIR", c.StateDir},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("--auth=oauth requires: %s", strings.Join(missing, ", "))
	}

	u, err := url.Parse(c.Issuer)
	if err != nil || u.Host == "" {
		return fmt.Errorf("BRIDGE_MCP_ISSUER %q is not a valid URL", c.Issuer)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil // local development only
	}
	return fmt.Errorf("BRIDGE_MCP_ISSUER must use https (got %q); http is allowed only on loopback", c.Issuer)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestConfig -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/config.go internal/oauth/config_test.go
git commit -m "feat(oauth): config with fail-fast validation"
```

---

## Task 6: Authorization-server metadata

**Files:**
- Create: `internal/oauth/metadata.go`
- Test: `internal/oauth/metadata_test.go`

**Interfaces:**
- Consumes: `Config`.
- Produces: `func (Config) authServerMetadata() map[string]any` and `func (*Server) handleAuthServerMetadata(http.ResponseWriter, *http.Request)`. (A plain map is used rather than `oauthex.AuthServerMeta` because that type is shaped for *parsing* a remote AS; emitting a map keeps the advertised field set explicit.)

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAuthServerMetadata_AdvertisesWhatClaudeNeeds(t *testing.T) {
	srv := &Server{cfg: validConfig()}
	rec := httptest.NewRecorder()

	srv.handleAuthServerMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var meta struct {
		Issuer                 string   `json:"issuer"`
		Authorization          string   `json:"authorization_endpoint"`
		Token                  string   `json:"token_endpoint"`
		Registration           string   `json:"registration_endpoint"`
		CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
		TokenEndpointAuthMeths []string `json:"token_endpoint_auth_methods_supported"`
		GrantTypes             []string `json:"grant_types_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, rec.Body)
	}

	if meta.Issuer != validConfig().Issuer {
		t.Errorf("issuer = %q, want %q", meta.Issuer, validConfig().Issuer)
	}
	if meta.Registration == "" {
		t.Error("registration_endpoint absent — Claude performs DCR and cannot connect without it")
	}
	if !slices.Contains(meta.CodeChallengeMethods, "S256") {
		t.Errorf("code_challenge_methods_supported = %v, want S256", meta.CodeChallengeMethods)
	}
	if slices.Contains(meta.CodeChallengeMethods, "plain") {
		t.Error("plain PKCE advertised; only S256 is accepted")
	}
	if !slices.Contains(meta.TokenEndpointAuthMeths, "none") {
		t.Errorf("token_endpoint_auth_methods_supported = %v, want none (Claude is a public client)", meta.TokenEndpointAuthMeths)
	}
	if !slices.Contains(meta.GrantTypes, "authorization_code") || !slices.Contains(meta.GrantTypes, "refresh_token") {
		t.Errorf("grant_types_supported = %v, want authorization_code and refresh_token", meta.GrantTypes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestAuthServerMetadata -v`
Expected: FAIL — `undefined: Server`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/oauth/server.go`:

```go
package oauth

import "net/http"

// Server exposes bridge's authorization-server endpoints. It is constructed by
// cmd/bridge and mounted outside the bearer middleware: a client cannot present
// a token it has not yet obtained.
type Server struct {
	cfg   Config
	store *Store
}

// NewServer returns a Server backed by store.
func NewServer(cfg Config, store *Store) *Server {
	return &Server{cfg: cfg, store: store}
}

// Handler returns the mux for the OAuth subtree and metadata documents.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleAuthServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.handleRegister)
	mux.HandleFunc("GET /oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("GET /oauth/callback", s.handleCallback)
	mux.HandleFunc("POST /oauth/token", s.handleToken)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // response already committed; nothing actionable remains
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
```

Add `"encoding/json"` to that file's imports. Create `internal/oauth/metadata.go`:

```go
package oauth

import (
	"net/http"
	"strings"
)

func (c Config) endpoint(path string) string {
	return strings.TrimRight(c.Issuer, "/") + path
}

// authServerMetadata is the RFC 8414 document. It advertises exactly the three
// capabilities authentik lacks and Claude requires: dynamic client
// registration, S256 PKCE, and public clients.
func (c Config) authServerMetadata() map[string]any {
	return map[string]any{
		"issuer":                                c.Issuer,
		"authorization_endpoint":                c.endpoint("/oauth/authorize"),
		"token_endpoint":                        c.endpoint("/oauth/token"),
		"registration_endpoint":                 c.endpoint("/oauth/register"),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.authServerMetadata())
}
```

Add temporary no-op stubs for the handlers referenced by `Handler()` so the package compiles; each is replaced in its own task:

```go
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request)  { http.NotFound(w, r) }
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request)  { http.NotFound(w, r) }
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request)     { http.NotFound(w, r) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestAuthServerMetadata -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/server.go internal/oauth/metadata.go internal/oauth/metadata_test.go
git commit -m "feat(oauth): authorization-server metadata document"
```

---

## Task 7: Dynamic client registration

**Files:**
- Create: `internal/oauth/register.go` (replaces the stub)
- Test: `internal/oauth/register_test.go`

**Interfaces:**
- Consumes: `Server`, `Store`, `Client`, `newSecret`, `enforceClientCap`.
- Produces: `func (*Server) handleRegister(http.ResponseWriter, *http.Request)`.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(validConfig(), store)
}

func TestHandleRegister_IssuesPublicClient(t *testing.T) {
	srv := newTestServer(t)
	body := `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`
	rec := httptest.NewRecorder()

	srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	var resp struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ClientID == "" {
		t.Error("client_id empty")
	}
	if resp.ClientSecret != "" {
		t.Error("client_secret issued; Claude registers as a public client")
	}

	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	if _, ok := srv.store.st.Clients[resp.ClientID]; !ok {
		t.Error("registration not persisted")
	}
}

func TestHandleRegister_RejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no redirect_uris", `{"client_name":"Claude"}`},
		{"empty redirect_uris", `{"redirect_uris":[]}`},
		{"malformed json", `{`},
		{"non-absolute redirect", `{"redirect_uris":["/relative"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			rec := httptest.NewRecorder()
			srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHandleRegister_EnforcesClientCap(t *testing.T) {
	srv := newTestServer(t)
	// Pre-fill at the cap with used registrations so eviction is observable.
	srv.store.mu.Lock()
	for i := 0; i < maxClients; i++ {
		srv.store.st.Clients[fmt.Sprintf("pre%03d", i)] = &Client{
			CreatedAt:  time.Now().Add(-time.Duration(maxClients-i) * time.Minute),
			LastUsedAt: time.Now().Add(-time.Duration(maxClients-i) * time.Minute),
		}
	}
	srv.store.mu.Unlock()

	rec := httptest.NewRecorder()
	srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"redirect_uris":["https://claude.ai/cb"]}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	if n := len(srv.store.st.Clients); n > maxClients {
		t.Errorf("client count = %d, exceeds cap %d (unbounded state growth is a DoS vector)", n, maxClients)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestHandleRegister -v`
Expected: FAIL — status 404 from the stub.

- [ ] **Step 3: Write minimal implementation**

Delete the `handleRegister` stub from `server.go` and create `internal/oauth/register.go`:

```go
package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// handleRegister implements RFC 7591 dynamic client registration. Claude assumes
// it is not pre-registered, so this endpoint is unauthenticated by spec. It is
// therefore internet-reachable: every registration is bounded by
// enforceClientCap to keep the state file from growing without limit.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body is not valid JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	for _, raw := range req.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be an absolute URL")
			return
		}
	}

	clientID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate client id")
		return
	}

	now := time.Now()
	s.store.mu.Lock()
	s.store.st.Clients[clientID] = &Client{
		RedirectURIs: req.RedirectURIs,
		Name:         req.ClientName,
		CreatedAt:    now,
	}
	s.store.enforceClientCap(now)
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist registration")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              req.RedirectURIs,
		"client_name":                req.ClientName,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestHandleRegister -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/register.go internal/oauth/register_test.go internal/oauth/server.go
git commit -m "feat(oauth): dynamic client registration with a bounded client table"
```

---

## Task 8: Authorize endpoint — redirect_uri and PKCE validation

**Files:**
- Create: `internal/oauth/authorize.go` (replaces the stub)
- Test: `internal/oauth/authorize_test.go`

**Interfaces:**
- Consumes: `Server`, `Store`, `Client`, `newSecret`.
- Produces: `func (*Server) handleAuthorize(http.ResponseWriter, *http.Request)`, `type loginSession`, and `s.sessions` (an in-memory `map[string]*loginSession` guarded by `s.sessMu`, holding the in-flight authentik round trip). Sessions are deliberately not persisted: an interrupted login is simply restarted.

This is the highest-risk task in the plan. `redirect_uri` matching must be exact.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

const registeredRedirect = "https://claude.ai/api/mcp/auth_callback"

// registerClient inserts a client directly, bypassing the HTTP layer.
func registerClient(t *testing.T, srv *Server, redirects ...string) string {
	t.Helper()
	id, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	srv.store.st.Clients[id] = &Client{RedirectURIs: redirects, CreatedAt: time.Now()}
	return id
}

func authorizeRequest(clientID, redirect, challenge, method string) *http.Request {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", "client-state")
	if challenge != "" {
		q.Set("code_challenge", challenge)
	}
	if method != "" {
		q.Set("code_challenge_method", method)
	}
	return httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
}

func TestHandleAuthorize_RedirectURIMustMatchExactly(t *testing.T) {
	tests := []struct {
		name     string
		redirect string
	}{
		{"prefix extension", registeredRedirect + ".evil.com"},
		{"extra path segment", registeredRedirect + "/extra"},
		{"trailing slash", registeredRedirect + "/"},
		{"scheme downgrade", "http://claude.ai/api/mcp/auth_callback"},
		{"different port", "https://claude.ai:8443/api/mcp/auth_callback"},
		{"added query", registeredRedirect + "?next=https://evil.com"},
		{"embedded userinfo", "https://claude.ai@evil.com/api/mcp/auth_callback"},
		{"host case variation", "https://CLAUDE.AI/api/mcp/auth_callback"},
		{"unrelated host", "https://evil.com/cb"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			rec := httptest.NewRecorder()

			srv.handleAuthorize(rec, authorizeRequest(cid, tt.redirect, "challenge-value", "S256"))

			// A bad redirect_uri must never be redirected to — that is the
			// open-redirect / code-theft path. Report it locally instead.
			if rec.Code == http.StatusFound || rec.Code == http.StatusSeeOther {
				t.Fatalf("redirected to unvalidated redirect_uri %q (Location %q)", tt.redirect, rec.Header().Get("Location"))
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for redirect_uri %q", rec.Code, tt.redirect)
			}
		})
	}
}

func TestHandleAuthorize_AcceptsExactMatchAndRedirectsToAuthentik(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}
	rec := httptest.NewRecorder()

	srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, "challenge-value", "S256"))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host != "auth.example.com" {
		t.Errorf("redirected to %q, want authentik", loc.Host)
	}
	if loc.Query().Get("state") == "" {
		t.Error("no state on the authentik leg; the callback could not be correlated")
	}
}

func TestHandleAuthorize_PKCERules(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		method    string
		wantErr   bool
	}{
		{"S256 accepted", "challenge-value", "S256", false},
		{"plain rejected", "challenge-value", "plain", true},
		{"missing method rejected", "challenge-value", "", true},
		{"missing challenge rejected", "", "S256", true},
		{"unknown method rejected", "challenge-value", "S512", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}
			rec := httptest.NewRecorder()

			srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, tt.challenge, tt.method))

			if tt.wantErr && rec.Code == http.StatusFound {
				t.Errorf("PKCE %q/%q accepted; want rejection", tt.challenge, tt.method)
			}
			if !tt.wantErr && rec.Code != http.StatusFound {
				t.Errorf("status = %d, want 302", rec.Code)
			}
		})
	}
}

func TestHandleAuthorize_UnknownClientRejected(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleAuthorize(rec, authorizeRequest("no-such-client", registeredRedirect, "challenge", "S256"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestHandleAuthorize -v`
Expected: FAIL — `undefined: authentikEndpoints`, 404 from the stub.

- [ ] **Step 3: Write minimal implementation**

Add to `server.go`: the `sessions`/`sessMu`/`authentik` fields and initialise the map in `NewServer`.

```go
// in server.go — Server struct becomes:
type Server struct {
	cfg       Config
	store     *Store
	authentik *authentikEndpoints

	sessMu   sync.Mutex
	sessions map[string]*loginSession
}

func NewServer(cfg Config, store *Store) *Server {
	return &Server{cfg: cfg, store: store, sessions: map[string]*loginSession{}}
}
```

(Add `"sync"` to `server.go` imports.) Delete the `handleAuthorize` stub and create `internal/oauth/authorize.go`:

```go
package oauth

import (
	"net/http"
	"net/url"
	"slices"
	"time"
)

// loginSession is one in-flight authorization: the client's original request,
// held while the user completes the authentik login. Sessions live in memory
// only — an interrupted login is simply restarted.
type loginSession struct {
	ClientID      string
	RedirectURI   string
	ClientState   string
	CodeChallenge string
	CreatedAt     time.Time
}

const loginSessionTTL = 10 * time.Minute

// handleAuthorize validates the client's request and hands the user off to
// authentik. Every failure here is reported locally rather than by redirecting:
// an unvalidated redirect_uri must never be used as a redirect target, since
// that is exactly the open-redirect and code-theft path.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if q.Get("response_type") != "code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type", "only response_type=code is supported")
		return
	}

	s.store.mu.Lock()
	client, known := s.store.st.Clients[q.Get("client_id")]
	var registered []string
	if known {
		registered = slices.Clone(client.RedirectURIs)
	}
	s.store.mu.Unlock()

	if !known {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}

	redirectURI := q.Get("redirect_uri")
	if !slices.Contains(registered, redirectURI) {
		// Exact string comparison. No normalisation, prefix, or wildcard
		// matching — those are the classic OAuth redirect bypasses.
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri does not exactly match a registered value")
		return
	}

	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}

	if s.authentik == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "identity provider metadata unavailable")
		return
	}

	sessionID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not start login")
		return
	}

	now := time.Now()
	s.sessMu.Lock()
	for id, sess := range s.sessions { // opportunistic cleanup
		if now.Sub(sess.CreatedAt) > loginSessionTTL {
			delete(s.sessions, id)
		}
	}
	s.sessions[sessionID] = &loginSession{
		ClientID:      q.Get("client_id"),
		RedirectURI:   redirectURI,
		ClientState:   q.Get("state"),
		CodeChallenge: challenge,
		CreatedAt:     now,
	}
	s.sessMu.Unlock()

	upstream, err := url.Parse(s.authentik.Authorization)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "invalid identity provider metadata")
		return
	}
	uq := upstream.Query()
	uq.Set("response_type", "code")
	uq.Set("client_id", s.cfg.ClientID)
	uq.Set("redirect_uri", s.cfg.endpoint("/oauth/callback"))
	uq.Set("scope", "openid profile")
	uq.Set("state", sessionID)
	upstream.RawQuery = uq.Encode()

	http.Redirect(w, r, upstream.String(), http.StatusFound)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestHandleAuthorize -race -v`
Expected: PASS — every redirect_uri near-miss returns 400, no redirect.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/authorize.go internal/oauth/authorize_test.go internal/oauth/server.go
git commit -m "feat(oauth): authorize endpoint with exact redirect_uri and S256 PKCE checks"
```

---

## Task 9: authentik OIDC client leg and the fake authentik

**Files:**
- Create: `internal/oauth/authentik.go`, `internal/oauth/fake_authentik_test.go`
- Test: `internal/oauth/authentik_test.go`

**Interfaces:**
- Consumes: `Config`.
- Produces:
  - `type authentikEndpoints struct { Authorization, Token, UserInfo string }`
  - `func discoverAuthentik(ctx context.Context, issuer string, c *http.Client) (*authentikEndpoints, error)`
  - `func (*Server) exchangeAndIdentify(ctx context.Context, code string) (subject string, err error)`
  - Test helper `func newFakeAuthentik(t *testing.T, subject string) *httptest.Server`.

Bridge fetches authentik's tokens directly from the token endpoint over TLS, so per OIDC Core §3.1.3.7 no ID-token signature validation is required — the design stays free of any JWT dependency. The subject comes from `userinfo`.

- [ ] **Step 1: Write the failing test**

Create the fake first (`fake_authentik_test.go`):

```go
package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newFakeAuthentik stands in for the identity provider: discovery, token
// exchange, and userinfo. Hand-rolled so the whole flow runs in-process.
func newFakeAuthentik(t *testing.T, subject string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"userinfo_endpoint":      srv.URL + "/userinfo",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") == "wrong-code" {
			http.Error(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "upstream-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sub": subject})
	})
	return srv
}
```

Then `authentik_test.go`:

```go
package oauth

import (
	"context"
	"testing"
)

func TestDiscoverAuthentik_ReadsEndpoints(t *testing.T) {
	fake := newFakeAuthentik(t, "sub-123")

	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatalf("discoverAuthentik: %v", err)
	}
	if eps.Token != fake.URL+"/token" {
		t.Errorf("Token = %q, want %q", eps.Token, fake.URL+"/token")
	}
	if eps.UserInfo != fake.URL+"/userinfo" {
		t.Errorf("UserInfo = %q, want %q", eps.UserInfo, fake.URL+"/userinfo")
	}
}

func TestExchangeAndIdentify(t *testing.T) {
	fake := newFakeAuthentik(t, "sub-123")
	srv := newTestServer(t)
	srv.httpClient = fake.Client()
	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv.authentik = eps

	t.Run("valid code yields the subject", func(t *testing.T) {
		sub, err := srv.exchangeAndIdentify(context.Background(), "good-code")
		if err != nil {
			t.Fatalf("exchangeAndIdentify: %v", err)
		}
		if sub != "sub-123" {
			t.Errorf("sub = %q, want sub-123", sub)
		}
	})

	t.Run("rejected code surfaces an error", func(t *testing.T) {
		if _, err := srv.exchangeAndIdentify(context.Background(), "wrong-code"); err == nil {
			t.Error("want error for a code authentik rejects")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run 'TestDiscoverAuthentik|TestExchangeAndIdentify' -v`
Expected: FAIL — `undefined: discoverAuthentik`.

- [ ] **Step 3: Write minimal implementation**

Add an `httpClient *http.Client` field to `Server` in `server.go`, defaulting in `NewServer`:

```go
func NewServer(cfg Config, store *Store) *Server {
	return &Server{
		cfg:        cfg,
		store:      store,
		sessions:   map[string]*loginSession{},
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}
```

(Add `"time"` to `server.go` imports.) Create `internal/oauth/authentik.go`:

```go
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// authentikEndpoints are the OIDC endpoints bridge uses as a confidential
// client of authentik.
type authentikEndpoints struct {
	Authorization string
	Token         string
	UserInfo      string
}

// discoverAuthentik reads the provider's OIDC discovery document.
func discoverAuthentik(ctx context.Context, issuer string, c *http.Client) (*authentikEndpoints, error) {
	metaURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", metaURL, err)
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close of a read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery %s: %s", metaURL, resp.Status)
	}

	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserInfoEndpoint      string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse discovery %s: %w", metaURL, err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.UserInfoEndpoint == "" {
		return nil, fmt.Errorf("discovery %s: missing required endpoints", metaURL)
	}
	return &authentikEndpoints{
		Authorization: doc.AuthorizationEndpoint,
		Token:         doc.TokenEndpoint,
		UserInfo:      doc.UserInfoEndpoint,
	}, nil
}

// exchangeAndIdentify swaps authentik's authorization code for an access token
// and resolves the authenticated subject via userinfo.
//
// The token is obtained directly from the token endpoint over TLS, so OIDC Core
// §3.1.3.7 does not require validating an ID token signature; using userinfo
// keeps this package free of any JWT dependency.
func (s *Server) exchangeAndIdentify(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.cfg.endpoint("/oauth/callback"))
	form.Set("client_id", s.cfg.ClientID)
	form.Set("client_secret", s.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authentik.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Deliberately does not include the body: it can echo the code.
		return "", fmt.Errorf("token exchange rejected: %s", resp.Status)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token response contained no access_token")
	}

	uiReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.authentik.UserInfo, nil)
	if err != nil {
		return "", fmt.Errorf("build userinfo request: %w", err)
	}
	uiReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	uiResp, err := s.httpClient.Do(uiReq)
	if err != nil {
		return "", fmt.Errorf("userinfo: %w", err)
	}
	defer func() { _ = uiResp.Body.Close() }()
	if uiResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo rejected: %s", uiResp.Status)
	}
	var ui struct {
		Sub string `json:"sub"`
	}
	if err := json.NewDecoder(uiResp.Body).Decode(&ui); err != nil {
		return "", fmt.Errorf("parse userinfo: %w", err)
	}
	if ui.Sub == "" {
		return "", fmt.Errorf("userinfo contained no sub")
	}
	return ui.Sub, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run 'TestDiscoverAuthentik|TestExchangeAndIdentify' -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/authentik.go internal/oauth/authentik_test.go internal/oauth/fake_authentik_test.go internal/oauth/server.go
git commit -m "feat(oauth): authentik OIDC client leg via userinfo"
```

---

## Task 10: Callback — subject check and code issuance

**Files:**
- Create: `internal/oauth/callback.go` (replaces the stub)
- Test: `internal/oauth/callback_test.go`

**Interfaces:**
- Consumes: `Server`, `loginSession`, `exchangeAndIdentify`, `Code`, `newSecret`, `hashSecret`.
- Produces: `func (*Server) handleCallback(http.ResponseWriter, *http.Request)`, `const authCodeTTL = 60 * time.Second`.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// startedSession wires a server to the fake IdP and seeds one in-flight login.
func startedSession(t *testing.T, subject string) (*Server, string) {
	t.Helper()
	fake := newFakeAuthentik(t, subject)
	srv := newTestServer(t)
	srv.httpClient = fake.Client()
	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv.authentik = eps

	cid := registerClient(t, srv, registeredRedirect)
	sessionID, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.sessions[sessionID] = &loginSession{
		ClientID: cid, RedirectURI: registeredRedirect,
		ClientState: "client-state", CodeChallenge: "challenge-value",
		CreatedAt: time.Now(),
	}
	return srv, sessionID
}

func callbackRequest(state, code string) *http.Request {
	q := url.Values{}
	q.Set("state", state)
	q.Set("code", code)
	return httptest.NewRequest(http.MethodGet, "/oauth/callback?"+q.Encode(), nil)
}

func TestHandleCallback_AllowedSubjectGetsRedirectedBackWithCode(t *testing.T) {
	srv, sessionID := startedSession(t, validConfig().AllowedSubject)
	rec := httptest.NewRecorder()

	srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != registeredRedirect {
		t.Errorf("redirected to %q, want %q", got, registeredRedirect)
	}
	if loc.Query().Get("code") == "" {
		t.Error("no authorization code returned to the client")
	}
	if loc.Query().Get("state") != "client-state" {
		t.Errorf("state = %q, want the client's original state", loc.Query().Get("state"))
	}
}

func TestHandleCallback_ForeignSubjectRejected(t *testing.T) {
	srv, sessionID := startedSession(t, "somebody-else")
	rec := httptest.NewRecorder()

	srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))

	if rec.Code == http.StatusFound {
		t.Fatal("a subject other than BRIDGE_OIDC_ALLOWED_SUB completed the login")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleCallback_UnknownAndReplayedState(t *testing.T) {
	t.Run("unknown state", func(t *testing.T) {
		srv, _ := startedSession(t, validConfig().AllowedSubject)
		rec := httptest.NewRecorder()
		srv.handleCallback(rec, callbackRequest("no-such-session", "good-code"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("replayed state", func(t *testing.T) {
		srv, sessionID := startedSession(t, validConfig().AllowedSubject)
		srv.handleCallback(httptest.NewRecorder(), callbackRequest(sessionID, "good-code"))

		rec := httptest.NewRecorder()
		srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))
		if rec.Code == http.StatusFound {
			t.Error("session replay succeeded; sessions must be single-use")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestHandleCallback -v`
Expected: FAIL — 404 from the stub.

- [ ] **Step 3: Write minimal implementation**

Delete the `handleCallback` stub and create `internal/oauth/callback.go`:

```go
package oauth

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const authCodeTTL = 60 * time.Second

// handleCallback completes the authentik leg: it consumes the login session,
// resolves the authenticated subject, enforces the single-user rule, and hands
// the client its own authorization code.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("state")

	// Single-use: take the session out of the map immediately.
	s.sessMu.Lock()
	sess, ok := s.sessions[sessionID]
	delete(s.sessions, sessionID)
	s.sessMu.Unlock()

	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown or already-used login session")
		return
	}
	if time.Since(sess.CreatedAt) > loginSessionTTL {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "login session expired; start again")
		return
	}
	if upstreamErr := r.URL.Query().Get("error"); upstreamErr != "" {
		slog.Warn("authentik returned an error", "client_id", sess.ClientID, "error", upstreamErr)
		writeOAuthError(w, http.StatusBadRequest, "access_denied", "identity provider returned an error")
		return
	}

	subject, err := s.exchangeAndIdentify(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		slog.Warn("authentik exchange failed", "client_id", sess.ClientID, "err", err)
		writeOAuthError(w, http.StatusBadGateway, "server_error", "identity provider exchange failed")
		return
	}
	if subject != s.cfg.AllowedSubject {
		slog.Warn("login rejected: subject not permitted",
			"client_id", sess.ClientID, "sub", subject, "allowed_sub", s.cfg.AllowedSubject)
		writeOAuthError(w, http.StatusForbidden, "access_denied", "this account is not permitted to use bridge")
		return
	}

	code, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue code")
		return
	}

	now := time.Now()
	s.store.mu.Lock()
	s.store.st.Codes[hashSecret(code)] = &Code{
		ClientID:      sess.ClientID,
		RedirectURI:   sess.RedirectURI,
		CodeChallenge: sess.CodeChallenge,
		Subject:       subject,
		ExpiresAt:     now.Add(authCodeTTL),
	}
	if c, ok := s.store.st.Clients[sess.ClientID]; ok {
		c.LastUsedAt = now
	}
	s.store.prune(now)
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist code")
		return
	}

	slog.Info("login accepted", "client_id", sess.ClientID, "sub", subject)

	dest, err := url.Parse(sess.RedirectURI)
	if err != nil { // already validated at /authorize; unreachable in practice
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "invalid stored redirect_uri")
		return
	}
	q := dest.Query()
	q.Set("code", code)
	if sess.ClientState != "" {
		q.Set("state", sess.ClientState)
	}
	dest.RawQuery = q.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestHandleCallback -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/callback.go internal/oauth/callback_test.go internal/oauth/server.go
git commit -m "feat(oauth): callback enforcing the single permitted subject"
```

---

## Task 11: Token endpoint — code grant with PKCE

**Files:**
- Create: `internal/oauth/token_endpoint.go` (replaces the stub)
- Test: `internal/oauth/token_endpoint_test.go`

**Interfaces:**
- Consumes: `Server`, `Store`, `Code`, `IssueTokenPair`, `hashSecret`.
- Produces: `func (*Server) handleToken(http.ResponseWriter, *http.Request)` handling `grant_type=authorization_code`, plus `func verifyPKCE(verifier, challenge string) bool`.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// seedCode inserts an authorization code directly.
func seedCode(t *testing.T, srv *Server, clientID, redirect, challenge string, expiry time.Time) string {
	t.Helper()
	code, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	srv.store.st.Codes[hashSecret(code)] = &Code{
		ClientID: clientID, RedirectURI: redirect,
		CodeChallenge: challenge, Subject: validConfig().AllowedSubject,
		ExpiresAt: expiry,
	}
	return code
}

func tokenRequest(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func codeGrantForm(clientID, code, verifier, redirect string) url.Values {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("client_id", clientID)
	f.Set("code", code)
	f.Set("code_verifier", verifier)
	f.Set("redirect_uri", redirect)
	return f
}

func TestHandleToken_CodeGrantIssuesTokens(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	const verifier = "the-verifier-value-must-be-long-enough"
	code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))

	rec := httptest.NewRecorder()
	srv.handleToken(rec, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", resp)
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn != int(accessTokenTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", resp.ExpiresIn, int(accessTokenTTL.Seconds()))
	}
}

func TestHandleToken_CodeGrantRejections(t *testing.T) {
	const verifier = "the-verifier-value-must-be-long-enough"

	tests := []struct {
		name    string
		mutate  func(t *testing.T, srv *Server, cid string, form url.Values)
		wantErr bool
	}{
		{
			name:   "happy path",
			mutate: func(*testing.T, *Server, string, url.Values) {},
		},
		{
			name:    "wrong verifier",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code_verifier", "wrong-verifier") },
			wantErr: true,
		},
		{
			name:    "missing verifier",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Del("code_verifier") },
			wantErr: true,
		},
		{
			name:    "redirect_uri mismatch",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("redirect_uri", "https://evil.com/cb") },
			wantErr: true,
		},
		{
			name:    "client_id mismatch",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("client_id", "another-client") },
			wantErr: true,
		},
		{
			name:    "unknown code",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code", "no-such-code") },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))
			form := codeGrantForm(cid, code, verifier, registeredRedirect)
			tt.mutate(t, srv, cid, form)

			rec := httptest.NewRecorder()
			srv.handleToken(rec, tokenRequest(form))

			if tt.wantErr && rec.Code == http.StatusOK {
				t.Errorf("request accepted; want rejection (body %s)", rec.Body)
			}
			if !tt.wantErr && rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
			}
		})
	}
}

func TestHandleToken_CodeIsSingleUseAndExpires(t *testing.T) {
	const verifier = "the-verifier-value-must-be-long-enough"

	t.Run("second use fails", func(t *testing.T) {
		srv := newTestServer(t)
		cid := registerClient(t, srv, registeredRedirect)
		code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))

		first := httptest.NewRecorder()
		srv.handleToken(first, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if first.Code != http.StatusOK {
			t.Fatalf("first exchange failed: %d %s", first.Code, first.Body)
		}

		second := httptest.NewRecorder()
		srv.handleToken(second, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if second.Code == http.StatusOK {
			t.Error("authorization code replay succeeded")
		}
	})

	t.Run("expired code fails", func(t *testing.T) {
		srv := newTestServer(t)
		cid := registerClient(t, srv, registeredRedirect)
		code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(-time.Second))

		rec := httptest.NewRecorder()
		srv.handleToken(rec, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if rec.Code == http.StatusOK {
			t.Error("expired code accepted")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestHandleToken -v`
Expected: FAIL — 404 from the stub.

- [ ] **Step 3: Write minimal implementation**

Delete the `handleToken` stub and create `internal/oauth/token_endpoint.go`:

```go
package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

// verifyPKCE reports whether verifier hashes to challenge under S256.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.handleAuthorizationCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (s *Server) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	var (
		clientID    = r.PostForm.Get("client_id")
		rawCode     = r.PostForm.Get("code")
		verifier    = r.PostForm.Get("code_verifier")
		redirectURI = r.PostForm.Get("redirect_uri")
		now         = time.Now()
	)

	s.store.mu.Lock()
	rec, ok := s.store.st.Codes[hashSecret(rawCode)]
	if ok {
		// Single-use: consume it regardless of whether validation passes, so a
		// failed attempt cannot be retried against the same code.
		delete(s.store.st.Codes, hashSecret(rawCode))
	}
	s.store.mu.Unlock()

	if !ok || !rec.ExpiresAt.After(now) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is unknown or expired")
		return
	}
	if rec.ClientID != clientID || rec.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was not issued for this client and redirect_uri")
		return
	}
	if !verifyPKCE(verifier, rec.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	chainID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	access, refresh, err := s.store.IssueTokenPair(rec.ClientID, rec.Subject, chainID, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
	})
}
```

Add a temporary stub for the refresh grant, replaced in Task 12:

```go
func (s *Server) handleRefreshGrant(w http.ResponseWriter, _ *http.Request) {
	writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "not implemented yet")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -run TestHandleToken -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/token_endpoint.go internal/oauth/token_endpoint_test.go internal/oauth/server.go
git commit -m "feat(oauth): authorization-code grant with S256 PKCE verification"
```

---

## Task 12: Refresh rotation and reuse detection

**Files:**
- Modify: `internal/oauth/token_endpoint.go` (replace the `handleRefreshGrant` stub)
- Test: `internal/oauth/token_endpoint_test.go` (append)

**Interfaces:**
- Consumes: `Store`, `Token`, `KindRefresh`, `IssueTokenPair`.
- Produces: `func (*Server) handleRefreshGrant(http.ResponseWriter, *http.Request)`, `func (*Store) revokeChain(chainID string)`.

- [ ] **Step 1: Write the failing test**

```go
func refreshForm(clientID, refresh string) url.Values {
	f := url.Values{}
	f.Set("grant_type", "refresh_token")
	f.Set("client_id", clientID)
	f.Set("refresh_token", refresh)
	return f
}

// exchangeCode runs a full code grant and returns the token pair.
func exchangeCode(t *testing.T, srv *Server, cid string) (string, string) {
	t.Helper()
	const verifier = "the-verifier-value-must-be-long-enough"
	code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))
	rec := httptest.NewRecorder()
	srv.handleToken(rec, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code grant failed: %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.AccessToken, resp.RefreshToken
}

func TestHandleRefreshGrant_RotatesTokens(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	_, refresh := exchangeCode(t, srv, cid)

	rec := httptest.NewRecorder()
	srv.handleToken(rec, tokenRequest(refreshForm(cid, refresh)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RefreshToken == refresh {
		t.Error("refresh token was not rotated")
	}
	if resp.AccessToken == "" {
		t.Error("no access token issued")
	}
}

func TestHandleRefreshGrant_ReuseRevokesWholeChain(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	_, original := exchangeCode(t, srv, cid)

	// Legitimate rotation.
	first := httptest.NewRecorder()
	srv.handleToken(first, tokenRequest(refreshForm(cid, original)))
	if first.Code != http.StatusOK {
		t.Fatalf("rotation failed: %d %s", first.Code, first.Body)
	}
	var rotated struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}

	// Replaying the consumed token indicates theft.
	replay := httptest.NewRecorder()
	srv.handleToken(replay, tokenRequest(refreshForm(cid, original)))
	if replay.Code == http.StatusOK {
		t.Fatal("consumed refresh token was accepted")
	}

	// The whole chain must now be dead, including the freshly rotated tokens.
	after := httptest.NewRecorder()
	srv.handleToken(after, tokenRequest(refreshForm(cid, rotated.RefreshToken)))
	if after.Code == http.StatusOK {
		t.Error("rotated refresh token still works after a reuse was detected")
	}
	if _, err := srv.store.Verifier()(context.Background(), rotated.AccessToken, nil); err == nil {
		t.Error("access token from the revoked chain still verifies")
	}
}
```

Add `"context"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestHandleRefreshGrant -v`
Expected: FAIL — "not implemented yet".

- [ ] **Step 3: Write minimal implementation**

Replace the stub in `token_endpoint.go`:

```go
// handleRefreshGrant rotates a refresh token. Presenting one that has already
// been consumed means the token leaked, so the entire chain is revoked rather
// than just refusing the request.
func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	var (
		clientID = r.PostForm.Get("client_id")
		raw      = r.PostForm.Get("refresh_token")
		now      = time.Now()
	)

	s.store.mu.Lock()
	rec, ok := s.store.st.Tokens[hashSecret(raw)]
	switch {
	case !ok || rec.Kind != KindRefresh:
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	case rec.Consumed:
		chain := rec.ChainID
		s.store.revokeChain(chain)
		saveErr := s.store.save()
		s.store.mu.Unlock()
		slog.Warn("refresh token reuse detected; chain revoked", "client_id", rec.ClientID, "sub", rec.Subject)
		if saveErr != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke chain")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected; re-authorization required")
		return
	case !rec.ExpiresAt.After(now):
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	case rec.ClientID != clientID:
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token was not issued to this client")
		return
	}

	rec.Consumed = true
	subject, chainID := rec.Subject, rec.ChainID
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not rotate token")
		return
	}

	access, refresh, err := s.store.IssueTokenPair(clientID, subject, chainID, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
	})
}
```

Add `"log/slog"` to the file's imports, and add `revokeChain` to `maintenance.go`:

```go
// revokeChain deletes every token sharing chainID. The caller must hold s.mu.
func (s *Store) revokeChain(chainID string) {
	for k, tok := range s.st.Tokens {
		if tok.ChainID == chainID {
			delete(s.st.Tokens, k)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oauth/ -race -v`
Expected: PASS — the whole package.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/token_endpoint.go internal/oauth/token_endpoint_test.go internal/oauth/maintenance.go
git commit -m "feat(oauth): refresh rotation with reuse detection"
```

---

## Task 13: Wire into cmd/bridge

**Files:**
- Modify: `cmd/bridge/mcp.go`
- Test: `cmd/bridge/mcp_test.go` (append)

**Interfaces:**
- Consumes: `oauth.Config`, `oauth.OpenStore`, `oauth.NewServer`, `(*oauth.Store).Verifier`, `oauth.DiscoverAuthentik`.
- Produces: `--auth` flag; `func buildOAuthHandler(srv *sdkmcp.Server, cfg oauth.Config) (http.Handler, func() error, error)`.

Export discovery for use from `cmd`: rename `discoverAuthentik` to `DiscoverAuthentik` and `authentikEndpoints` to `AuthentikEndpoints`, updating Task 9's call sites and adding a `Server.SetAuthentik(*AuthentikEndpoints)` setter (the field is unexported).

- [ ] **Step 1: Write the failing test**

```go
func TestBuildOAuthHandler_RoutesAndMiddlewarePlacement(t *testing.T) {
	dir := t.TempDir()
	cfg := imcpoauth.Config{
		Issuer:          "https://bridge-mcp.example.com",
		AuthentikIssuer: "https://auth.example.com/application/o/bridge/",
		ClientID:        "cid",
		ClientSecret:    "secret",
		AllowedSubject:  "sub-123",
		StateDir:        dir,
	}
	srv := imcp.NewServer(imcp.Deps{})

	handler, closeFn, err := buildOAuthHandler(srv, cfg)
	if err != nil {
		t.Fatalf("buildOAuthHandler: %v", err)
	}
	defer closeFn()

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"AS metadata is unauthenticated", "/.well-known/oauth-authorization-server", http.StatusOK},
		{"resource metadata is unauthenticated", "/.well-known/oauth-protected-resource", http.StatusOK},
		{"MCP root requires a token", "/", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s = %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestBuildMCPHandler_StaticModeUnchanged(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{})

	if _, err := buildMCPHandler(srv, "", false); err == nil {
		t.Error("want an error when a token is required but empty")
	}
	if _, err := buildMCPHandler(srv, "tok", false); err != nil {
		t.Errorf("static mode with a token: %v", err)
	}
	if _, err := buildMCPHandler(srv, "", true); err != nil {
		t.Errorf("--no-auth mode: %v", err)
	}
}
```

Import the oauth package in the test as `imcpoauth "github.com/freaxnx01/bridge/internal/oauth"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge/ -run 'TestBuildOAuthHandler|TestBuildMCPHandler_StaticMode' -v`
Expected: FAIL — `undefined: buildOAuthHandler`.

- [ ] **Step 3: Write minimal implementation**

In `cmd/bridge/mcp.go`, add the flag and wiring:

```go
// add to the var block
var mcpAuthMode string

// in newMCPCmd, alongside the other flags
serveCmd.Flags().StringVar(&mcpAuthMode, "auth", "static", "auth mode: static (bearer token) or oauth")
```

Add the handler builder:

```go
// buildOAuthHandler mounts the MCP transport behind OAuth bearer auth and the
// authorization-server endpoints beside it. The OAuth and metadata routes sit
// outside the bearer middleware: a client cannot present a token it has not yet
// obtained. Returns a close func releasing the state-directory lock.
func buildOAuthHandler(srv *sdkmcp.Server, cfg oauth.Config) (http.Handler, func() error, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	store, err := oauth.OpenStore(cfg.StateDir)
	if err != nil {
		return nil, nil, err
	}

	authServer := oauth.NewServer(cfg, store)

	// Discovery is best-effort at startup: an authentik outage must not stop
	// bridge from serving already-issued tokens.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if eps, err := oauth.DiscoverAuthentik(ctx, cfg.AuthentikIssuer, http.DefaultClient); err != nil {
		slog.Warn("authentik discovery failed; new logins will be unavailable until it recovers", "err", err)
	} else {
		authServer.SetAuthentik(eps)
	}

	resourceMeta := sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             cfg.Issuer,
		AuthorizationServers: []string{cfg.Issuer},
	})

	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
	guarded := sdkauth.RequireBearerToken(store.Verifier(), &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: strings.TrimRight(cfg.Issuer, "/") + "/.well-known/oauth-protected-resource",
	})(streamable)

	mux := http.NewServeMux()
	mux.Handle("/.well-known/oauth-protected-resource", resourceMeta)
	mux.Handle("/.well-known/oauth-authorization-server", authServer.Handler())
	mux.Handle("/oauth/", authServer.Handler())
	mux.Handle("/", guarded)

	return mux, store.Close, nil
}
```

In `runMCPServe`, branch on the mode:

```go
	var (
		handler http.Handler
		cleanup = func() error { return nil }
	)
	switch mcpAuthMode {
	case "static":
		handler, err = buildMCPHandler(srv, os.Getenv("BRIDGE_MCP_TOKEN"), mcpNoAuth)
	case "oauth":
		handler, cleanup, err = buildOAuthHandler(srv, oauth.Config{
			Issuer:          os.Getenv("BRIDGE_MCP_ISSUER"),
			AuthentikIssuer: os.Getenv("BRIDGE_OIDC_ISSUER"),
			ClientID:        os.Getenv("BRIDGE_OIDC_CLIENT_ID"),
			ClientSecret:    os.Getenv("BRIDGE_OIDC_CLIENT_SECRET"),
			AllowedSubject:  os.Getenv("BRIDGE_OIDC_ALLOWED_SUB"),
			StateDir:        mcpStateDir(),
		})
	default:
		return fmt.Errorf("--auth must be static or oauth, got %q", mcpAuthMode)
	}
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
```

Add the state-dir resolver:

```go
// mcpStateDir resolves the OAuth state directory: the env override wins,
// otherwise ~/.local/state/bridge-mcp.
func mcpStateDir() string {
	if dir := os.Getenv("BRIDGE_MCP_STATE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "bridge-mcp")
}
```

Add imports to `cmd/bridge/mcp.go`: `"path/filepath"`, `"strings"` (already present), `"github.com/modelcontextprotocol/go-sdk/oauthex"`, and `"github.com/freaxnx01/bridge/internal/oauth"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/bridge/ -race -v -run 'TestBuildOAuthHandler|TestBuildMCPHandler'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/mcp.go cmd/bridge/mcp_test.go internal/oauth/
git commit -m "feat(mcp): --auth=oauth wiring with OAuth routes outside the bearer guard"
```

---

## Task 14: End-to-end integration test

**Files:**
- Create: `internal/oauth/integration_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing — this task adds only a test.

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestEndToEnd_FullConnectorFlow walks the path a Claude connector takes:
// discovery, dynamic registration, authorization, the authentik login, the
// token exchange, and finally an authenticated call.
func TestEndToEnd_FullConnectorFlow(t *testing.T) {
	fake := newFakeAuthentik(t, "sub-123")

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	cfg := validConfig()
	cfg.AllowedSubject = "sub-123"
	authServer := NewServer(cfg, store)
	authServer.httpClient = fake.Client()

	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	authServer.authentik = eps

	front := httptest.NewServer(authServer.Handler())
	defer front.Close()
	cfg.Issuer = front.URL
	authServer.cfg = cfg

	// 1. AS metadata advertises DCR.
	metaResp, err := front.Client().Get(front.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	defer metaResp.Body.Close()
	var meta struct {
		Registration string `json:"registration_endpoint"`
	}
	if err := json.NewDecoder(metaResp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.Registration == "" {
		t.Fatal("no registration_endpoint advertised")
	}

	// 2. Dynamic client registration.
	regResp, err := front.Client().Post(front.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["`+registeredRedirect+`"],"client_name":"Claude"}`))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer regResp.Body.Close()
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}

	// 3. Authorize — do not follow the redirect to authentik.
	noRedirect := front.Client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	const verifier = "the-verifier-value-must-be-long-enough"
	authURL := front.URL + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {registeredRedirect},
		"state":                 {"client-state"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode()

	authResp, err := noRedirect.Get(authURL)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", authResp.StatusCode)
	}
	upstream, err := url.Parse(authResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse upstream redirect: %v", err)
	}
	sessionID := upstream.Query().Get("state")

	// 4. authentik returns the user to the callback.
	cbResp, err := noRedirect.Get(front.URL + "/oauth/callback?" + url.Values{
		"state": {sessionID}, "code": {"good-code"},
	}.Encode())
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", cbResp.StatusCode)
	}
	back, err := url.Parse(cbResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	code := back.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code returned")
	}
	if got := back.Query().Get("state"); got != "client-state" {
		t.Errorf("state = %q, want client-state", got)
	}

	// 5. Token exchange.
	tokResp, err := front.Client().PostForm(front.URL+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {reg.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {registeredRedirect},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", tokResp.StatusCode)
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}

	// 6. The access token authenticates at the resource server.
	info, err := store.Verifier()(context.Background(), tokens.AccessToken, nil)
	if err != nil {
		t.Fatalf("issued access token rejected: %v", err)
	}
	if info.UserID != "sub-123" {
		t.Errorf("UserID = %q, want sub-123", info.UserID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oauth/ -run TestEndToEnd -v`
Expected: initially FAIL if any earlier task is incomplete; it exercises the whole chain.

- [ ] **Step 3: Fix whatever it surfaces**

No new production code should be required. If the test fails, the defect is in an earlier task — fix it there rather than weakening the test.

- [ ] **Step 4: Run the full suite**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: `gofmt` prints nothing; vet, lint, and all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/integration_test.go
git commit -m "test(oauth): end-to-end connector flow against a fake authentik"
```

---

## Task 15: Documentation

**Files:**
- Modify: `CHANGELOG.md`, `README.md`

- [ ] **Step 1: Add the changelog entry**

Under `## [Unreleased]` → `### Added`:

```markdown
- `bridge mcp serve --auth=oauth`: OAuth 2.1 authorization-server mode so Claude
  custom connectors (web and mobile) can authenticate. Bridge performs dynamic
  client registration and PKCE itself, delegating the human login to authentik
  via OIDC. Tokens are opaque and stored hashed; `--auth=static` remains the
  default and is unchanged.
```

- [ ] **Step 2: Document the configuration in README.md**

Add to the MCP section: the `--auth` flag, the six environment variables from the
Config table with one-line descriptions, and this note:

```markdown
> `--auth=oauth` is only reachable by Claude connectors if the endpoint is
> published to the internet. Doing so means the `read_file` tool is exposed to
> the public network, guarded solely by this OAuth implementation. Review the
> threat model in the design doc before enabling it.
```

- [ ] **Step 3: Verify the docs build**

Run: `gofmt -l . && go test -race ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs(mcp): document --auth=oauth and its configuration"
```

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| `internal/oauth` package, three roles | 1–14 |
| File-backed store, 0600/0700, atomic write | 1 |
| Corrupt state refuses to start | 1 |
| Single-instance lock | 2 |
| Prune, 100-client cap, 24h unused TTL | 3, 7 |
| Opaque tokens, SHA-256 hashed, 1h/30d | 4 |
| `Verifier()` returning `auth.TokenVerifier`, `UserID` set | 4 |
| Config with fail-fast, https-except-loopback | 5 |
| AS metadata: DCR, S256-only, public clients | 6 |
| DCR endpoint | 7 |
| Exact `redirect_uri` match (8 near-misses) | 8 |
| PKCE S256 required, `plain` rejected | 8, 11 |
| authentik discovery/exchange/userinfo, no JWT | 9 |
| Single permitted `sub` | 10 |
| Codes single-use, 60s, bound to client+redirect+challenge | 10, 11 |
| Refresh rotation, reuse revokes chain | 12 |
| Mux with OAuth outside the bearer guard | 13 |
| `--auth=static` default and unchanged | 13 |
| End-to-end integration test | 14 |
| Never log secrets | 10, 12 (and the global constraint) |
| Deployment (Cloudflare, whitelist) — out of scope | — |

**Placeholder scan:** none. Every code step contains complete, compilable code.

**Type consistency:** `Client`/`Code`/`Token`/`TokenKind` (Task 1) are used unchanged
throughout. `newSecret`/`hashSecret` (Task 4) are used by Tasks 7, 8, 10, 11, 12.
`loginSession` (Task 8) is consumed by Task 10. `authentikEndpoints` is introduced in
Task 8's test and defined in Task 9 — Task 8's test therefore requires Task 9's type,
so **implement Task 9 before running Task 8's tests**, or temporarily declare the
struct in Task 8. It is exported as `AuthentikEndpoints` in Task 13 along with
`DiscoverAuthentik` and the new `SetAuthentik` setter; Task 9's call sites are renamed
at that point.

**Known ordering constraints** (call these out to whoever executes the plan):
1. Task 1's `OpenStore` needs Task 2's `acquireLock` — a stub is provided.
2. Task 8's tests need Task 9's `authentikEndpoints`.
3. Task 6 stubs the four handlers; Tasks 7, 8, 10, 11 each delete their own stub.
