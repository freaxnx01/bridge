package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestWriteError_SetsStatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad input")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad input") {
		t.Errorf("body missing message: %s", w.Body.String())
	}
}

func TestHttpStatus_ErrRepoExists_Conflict(t *testing.T) {
	if got := httpStatus(forge.ErrRepoExists); got != http.StatusConflict {
		t.Errorf("httpStatus(ErrRepoExists) = %d, want 409", got)
	}
}

func TestHttpStatus_Generic_InternalServerError(t *testing.T) {
	if got := httpStatus(fmt.Errorf("boom")); got != http.StatusInternalServerError {
		t.Errorf("httpStatus(generic) = %d, want 500", got)
	}
}
