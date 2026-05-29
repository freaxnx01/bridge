package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func setCache(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("BRIDGE_CACHE", d)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv(disableEnv, "")
	return d
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in            string
		maj, min, pat int
		ok            bool
	}{
		{"v2.2.0", 2, 2, 0, true},
		{"2.2.0", 2, 2, 0, true},
		{"v2.2.0-1-gabcdef", 2, 2, 0, true},
		{"v2.2.0-dirty", 2, 2, 0, true},
		{"v2.2.0+meta", 2, 2, 0, true},
		{" v2.2.0 ", 2, 2, 0, true},
		{"dev", 0, 0, 0, false},
		{"v2.2", 0, 0, 0, false},
		{"vX.Y.Z", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	}
	for _, c := range cases {
		maj, min, pat, ok := parseSemver(c.in)
		if ok != c.ok || maj != c.maj || min != c.min || pat != c.pat {
			t.Errorf("parseSemver(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				c.in, maj, min, pat, ok, c.maj, c.min, c.pat, c.ok)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v2.2.0", "v2.2.1", true},
		{"v2.2.0", "v2.3.0", true},
		{"v2.2.0", "v3.0.0", true},
		{"v2.2.0", "v2.2.0", false},
		{"v2.2.1", "v2.2.0", false},
		{"v2.2.0-1-gabc", "v2.2.0", false}, // same X.Y.Z, no hint
		{"v2.2.0-1-gabc", "v2.2.1", true},
		{"dev", "v2.2.0", false},
		{"v2.2.0", "", false},
		{"", "v2.2.0", false},
	}
	for _, c := range cases {
		got := Newer(c.current, c.latest)
		if got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestReadWriteRoundtrip(t *testing.T) {
	setCache(t)
	when := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := Write("v2.3.0", when); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tag, ts, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tag != "v2.3.0" || !ts.Equal(when) {
		t.Errorf("got (%q, %v), want (v2.3.0, %v)", tag, ts, when)
	}
}

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/freaxnx01/bridge/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
	}))
	defer srv.Close()
	tag, err := Fetch(context.Background(), srv.URL, "freaxnx01", "bridge")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if tag != "v9.9.9" {
		t.Errorf("tag = %q", tag)
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "x", "y"); err == nil {
		t.Errorf("expected error on 403")
	}
}

func TestMaybeRefreshFreshCacheSkipsNetwork(t *testing.T) {
	setCache(t)
	if err := Write("v2.2.0", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
	}))
	defer srv.Close()
	MaybeRefresh(context.Background(), "v2.2.0", srv.URL)
	if hits != 0 {
		t.Errorf("expected 0 network hits with fresh cache, got %d", hits)
	}
}

func TestMaybeRefreshStaleCacheRefetches(t *testing.T) {
	setCache(t)
	if err := Write("v2.1.0", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.3.0"})
	}))
	defer srv.Close()
	MaybeRefresh(context.Background(), "v2.2.0", srv.URL)
	tag, _, err := Read()
	if err != nil {
		t.Fatalf("Read after refresh: %v", err)
	}
	if tag != "v2.3.0" {
		t.Errorf("cache tag = %q, want v2.3.0", tag)
	}
}

func TestMaybeRefreshDevSkips(t *testing.T) {
	setCache(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	MaybeRefresh(context.Background(), "dev", srv.URL)
	if hits != 0 {
		t.Errorf("expected 0 hits for dev build, got %d", hits)
	}
}

func TestMaybeRefreshOptOutSkips(t *testing.T) {
	setCache(t)
	t.Setenv(disableEnv, "1")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	MaybeRefresh(context.Background(), "v2.2.0", srv.URL)
	if hits != 0 {
		t.Errorf("expected 0 hits with opt-out, got %d", hits)
	}
}

func TestHintRendersWhenNewer(t *testing.T) {
	setCache(t)
	if err := Write("v2.5.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := Hint("v2.2.0")
	if !strings.Contains(got, "v2.5.0") {
		t.Errorf("Hint missing tag: %q", got)
	}
	if !strings.Contains(got, "github.com/freaxnx01/bridge") {
		t.Errorf("Hint missing URL: %q", got)
	}
}

func TestHintEmptyWhenUpToDate(t *testing.T) {
	setCache(t)
	if err := Write("v2.2.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := Hint("v2.2.0"); got != "" {
		t.Errorf("expected empty hint, got %q", got)
	}
}

func TestHintEmptyWhenNoCache(t *testing.T) {
	setCache(t)
	if got := Hint("v2.2.0"); got != "" {
		t.Errorf("expected empty hint, got %q", got)
	}
}

func TestHintEmptyForDev(t *testing.T) {
	setCache(t)
	_ = Write("v9.9.9", time.Now())
	if got := Hint("dev"); got != "" {
		t.Errorf("expected empty hint for dev, got %q", got)
	}
}

func TestFetchTimeoutEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "vX"})
	}))
	defer srv.Close()
	setCache(t)
	start := time.Now()
	MaybeRefresh(context.Background(), "v2.2.0", srv.URL)
	if d := time.Since(start); d > FetchTimeout+500*time.Millisecond {
		t.Errorf("MaybeRefresh blocked too long: %v", d)
	}
	// Cache should be empty (fetch timed out).
	if _, err := os.Stat(CachePath()); !os.IsNotExist(err) {
		t.Errorf("cache should not exist after timeout, stat=%v", err)
	}
}

func TestFetchAPIBaseDefault(t *testing.T) {
	// Smoke: empty apiBase falls back to github.com — we don't actually call
	// it, just confirm the URL formatting doesn't double-slash. Use a stub
	// server only to capture the path the request would have used; pass it
	// via env-style baseURL with trailing slash to exercise the trim.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/a/b/releases/latest" {
			t.Errorf("path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"tag_name":"v0.0.1"}`)
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL+"/", "a", "b"); err != nil {
		t.Errorf("Fetch: %v", err)
	}
}
