package forge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ListOpenIssues' error status is typed (for token invalidation) and keeps
// its text.
func TestListOpenIssues_ErrorStatusIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad token"))
	}))
	defer srv.Close()
	clients := map[string]Client{
		"github":  NewGithubClient("t", srv.URL),
		"gitlab":  NewGitlabClient("t", srv.URL),
		"forgejo": NewForgejoClient("t", srv.URL),
	}
	for name, c := range clients {
		_, err := c.ListOpenIssues(context.Background(), "o", "r")
		var he *HTTPError
		if !errors.As(err, &he) || he.StatusCode != http.StatusUnauthorized || he.Forge != name {
			t.Errorf("%s: err = %v (%T), want *HTTPError 401", name, err, err)
			continue
		}
		if !IsAuthError(err) || err.Error() != name+" "+he.Path+": 401 Unauthorized: bad token" {
			t.Errorf("%s: IsAuthError=%v text=%q", name, IsAuthError(err), err.Error())
		}
	}
	if IsAuthError(&HTTPError{StatusCode: http.StatusNotFound}) || IsAuthError(errors.New("x")) {
		t.Error("IsAuthError true for a non-auth error")
	}
}
