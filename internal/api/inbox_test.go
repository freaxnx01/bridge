package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/dispatch"
)

// detailCall is the (owner, repo, number) the handler parsed out of the path.
type detailCall struct {
	owner, repo string
	number      int
}

func newInboxFixture(inbox cockpit.Inbox, detail cockpit.IssueDetail, err error) (*InboxHandler, *[]detailCall) {
	var calls []detailCall
	h := &InboxHandler{
		Inbox: func() cockpit.Inbox { return inbox },
		Detail: func(_ context.Context, owner, repo string, number int) (cockpit.IssueDetail, error) {
			calls = append(calls, detailCall{owner, repo, number})
			return detail, err
		},
	}
	return h, &calls
}

func TestInboxHandler_GetInbox_ReturnsJSON(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{
		Cards: []cockpit.InboxCard{{Repo: "o/r", Number: 412, Kind: cockpit.CardHuman, Reason: "review blocked"}},
	}, cockpit.IssueDetail{}, nil)

	w := doReq(h, http.MethodGet, "/api/inbox", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got cockpit.Inbox
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cards) != 1 || got.Cards[0].Reason != "review blocked" {
		t.Errorf("cards = %+v", got.Cards)
	}
}

func TestInboxHandler_EmptyInbox_SerializesEmptyArrays(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, nil)

	body := doReq(h, http.MethodGet, "/api/inbox", "").Body.String()

	for _, want := range []string{`"cards":[]`, `"waiting":[]`, `"parked":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s does not contain %s", body, want)
		}
	}
}

func TestInboxHandler_GetDetail_PassesOwnerRepoNumber(t *testing.T) {
	detail := cockpit.IssueDetail{
		Issue: cockpit.IssueRef{Repo: "freaxnx01/bridge", Number: 412, Labels: []string{}},
		Runs:  []dispatch.RunRecord{{Class: "task_failure"}},
	}
	h, calls := newInboxFixture(cockpit.Inbox{}, detail, nil)

	w := doReq(h, http.MethodGet, "/api/inbox/freaxnx01/bridge/412", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	if len(*calls) != 1 || (*calls)[0] != (detailCall{"freaxnx01", "bridge", 412}) {
		t.Fatalf("detail calls = %+v", *calls)
	}
	if !strings.Contains(w.Body.String(), `"runs":[`) {
		t.Errorf("body %s, want a runs array", w.Body)
	}
}

func TestInboxHandler_DetailUnknownRepo_404(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, cockpit.ErrUnknownRepo)

	w := doReq(h, http.MethodGet, "/api/inbox/o/r/1", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestInboxHandler_DetailForgeError_502GenericBody(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, errors.New("token 12345 rejected"))

	w := doReq(h, http.MethodGet, "/api/inbox/o/r/1", "")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if strings.Contains(w.Body.String(), "12345") {
		t.Errorf("body %s leaks the forge error", w.Body)
	}
}

func TestInboxHandler_BadNumber_400(t *testing.T) {
	h, calls := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, nil)

	for _, number := range []string{"abc", "0", "-1"} {
		t.Run(number, func(t *testing.T) {
			w := doReq(h, http.MethodGet, "/api/inbox/o/r/"+number, "")
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("detail calls = %+v, want none", *calls)
	}
}

func TestInboxHandler_MalformedPath_404(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, nil)

	for _, path := range []string{"/api/inbox/o/r", "/api/inbox/o/r/1/x", "/api/inbox/o//1", "/api/inbox/"} {
		t.Run(path, func(t *testing.T) {
			if w := doReq(h, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", w.Code)
			}
		})
	}
}

func TestInboxHandler_WrongMethod_405(t *testing.T) {
	h, _ := newInboxFixture(cockpit.Inbox{}, cockpit.IssueDetail{}, nil)

	for _, path := range []string{"/api/inbox", "/api/inbox/o/r/1"} {
		t.Run(path, func(t *testing.T) {
			w := doReq(h, http.MethodPost, path, "")
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", w.Code)
			}
			if got := w.Header().Get("Allow"); got != http.MethodGet {
				t.Errorf("Allow = %q, want GET", got)
			}
		})
	}
}
