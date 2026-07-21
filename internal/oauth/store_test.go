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
