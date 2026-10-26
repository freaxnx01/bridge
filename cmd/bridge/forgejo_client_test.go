package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

// mustTarget fails the test BEFORE any request unless c points at base, so a
// regression can never send a request to a real forge (codeberg.org).
func mustTarget(t *testing.T, c forge.Client, base string) {
	t.Helper()
	if got := fmt.Sprintf("%#v", c); !strings.Contains(got, `baseURL:"`+base+`"`) {
		t.Fatalf("client %s does not target %s; refusing to send a request", got, base)
	}
}

// forgejoRoot builds a repos root whose git-forgejo/.envrc holds envrc, and
// approves it in an isolated direnv store. Skips without direnv.
func forgejoRoot(t *testing.T, envrc string) string {
	t.Helper()
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "git-forgejo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte(envrc), 0o644); err != nil {
		t.Fatal(err)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "cfg"))
	if out, err := exec.Command("direnv", "allow", dir).CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v: %s", err, out)
	}
	t.Setenv("FORGEJO_TOKEN", "")
	t.Setenv("BRIDGE_FORGEJO_API", "")
	return root
}

// fakeForgejo records the Authorization header of every request.
func fakeForgejo(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

// The API base comes from the same direnv scope as the token.
func TestClientForMCP_ForgejoUsesDirenvAPIBase(t *testing.T) {
	srv, auth := fakeForgejo(t)
	root := forgejoRoot(t, "export FORGEJO_TOKEN=fj-tok\nexport BRIDGE_FORGEJO_API="+srv.URL+"\n")
	c := clientForMCP([]string{root})("forgejo", "freax")
	if c == nil {
		t.Fatal("no client although .envrc has token and API base")
	}
	mustTarget(t, c, srv.URL)
	if _, err := c.ListOpenIssues(context.Background(), "freax", "r"); err != nil {
		t.Fatal(err)
	}
	if len(*auth) != 1 || (*auth)[0] != "token fj-tok" {
		t.Errorf("requests to the .envrc API base = %v, want one with the token", *auth)
	}
}

// A set BRIDGE_FORGEJO_API wins (the bridge-mcp service sets it).
func TestClientForMCP_ForgejoEnvAPIBaseWins(t *testing.T) {
	envSrv, envAuth := fakeForgejo(t)
	rcSrv, rcAuth := fakeForgejo(t)
	root := forgejoRoot(t, "export FORGEJO_TOKEN=fj-tok\nexport BRIDGE_FORGEJO_API="+rcSrv.URL+"\n")
	t.Setenv("BRIDGE_FORGEJO_API", envSrv.URL)
	c := clientForMCP([]string{root})("forgejo", "freax")
	if c == nil {
		t.Fatal("no client")
	}
	mustTarget(t, c, envSrv.URL)
	if _, err := c.ListOpenIssues(context.Background(), "freax", "r"); err != nil {
		t.Fatal(err)
	}
	if len(*envAuth) != 1 || len(*rcAuth) != 0 {
		t.Errorf("env server hits %d, .envrc server hits %d; want 1 and 0", len(*envAuth), len(*rcAuth))
	}
}

// No API base anywhere: no client at all — never the public codeberg.org
// default with a self-hosted token. (A nil client cannot send a request.)
func TestClientForMCP_ForgejoWithoutAPIBaseIsNil(t *testing.T) {
	root := forgejoRoot(t, "export FORGEJO_TOKEN=fj-tok\n")
	if c := clientForMCP([]string{root})("forgejo", "freax"); c != nil {
		t.Errorf("client = %#v, want nil without an API base", c)
	}
}

func TestClientFor_ForgejoTokenWithoutAPIBaseIsNil(t *testing.T) {
	t.Setenv("FORGEJO_TOKEN", "fj-tok")
	t.Setenv("BRIDGE_FORGEJO_API", "")
	if c := clientFor("forgejo"); c != nil {
		t.Errorf("client = %#v, want nil without an API base", c)
	}
	srv, auth := fakeForgejo(t)
	t.Setenv("BRIDGE_FORGEJO_API", srv.URL)
	c := clientFor("forgejo")
	if c == nil {
		t.Fatal("no client with token and API base")
	}
	mustTarget(t, c, srv.URL)
	if _, err := c.ListOpenIssues(context.Background(), "freax", "r"); err != nil || len(*auth) != 1 {
		t.Errorf("err %v, requests %v", err, *auth)
	}
}
