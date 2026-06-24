package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func fakeRepos() []core.Repo {
	return []core.Repo{
		{Owner: "alice", Name: "myrepo", Forge: "github"},
	}
}

func TestReposHandler_List_ReturnsRepos(t *testing.T) {
	h := &ReposHandler{Discover: func() ([]core.Repo, error) { return fakeRepos(), nil }}
	r := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []core.Repo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "myrepo" {
		t.Errorf("got repos = %+v", got)
	}
}

func TestReposHandler_Detail_ReturnsRepoDetail(t *testing.T) {
	h := &ReposHandler{
		Discover: func() ([]core.Repo, error) { return fakeRepos(), nil },
		Issues: func(_ context.Context, _, _, _ string) ([]forge.Issue, error) {
			return []forge.Issue{{Title: "open bug"}}, nil
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/repos/alice/myrepo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got RepoDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Repo.Name != "myrepo" {
		t.Errorf("got repo name = %q", got.Repo.Name)
	}
	if len(got.Issues) != 1 || got.Issues[0].Title != "open bug" {
		t.Errorf("got issues = %+v", got.Issues)
	}
}

func TestReposHandler_Detail_NotFound_Returns404(t *testing.T) {
	h := &ReposHandler{Discover: func() ([]core.Repo, error) { return fakeRepos(), nil }}
	r := httptest.NewRequest(http.MethodGet, "/api/repos/nobody/nope", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestReposHandler_Create_Returns201(t *testing.T) {
	var notified string
	h := &ReposHandler{
		Discover: func() ([]core.Repo, error) { return fakeRepos(), nil },
		Create: func(_ context.Context, name, _ string, _ bool) (core.Repo, error) {
			return core.Repo{Name: name, Owner: "alice", Forge: "github"}, nil
		},
		Notify: func(eventType string, _ any) { notified = eventType },
	}
	body := strings.NewReader(`{"name":"newrepo","forge":"github","private":true}`)
	r := httptest.NewRequest(http.MethodPost, "/api/repos", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if notified != "overview-updated" {
		t.Errorf("Notify called with %q, want overview-updated", notified)
	}
}
