package agentview

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.out, f.err
}

func TestList_ValidArray_ParsesSortsAndConvertsEpoch(t *testing.T) {
	// startedAt is epoch-ms. 1783094237071 = 2026-07-01T... ; exact instant asserted below.
	raw := `[
	  {"pid":2,"cwd":"/home/u/b","kind":"interactive","startedAt":1000,"sessionId":"s-idle","name":"zeta","status":"idle"},
	  {"pid":1,"cwd":"/home/u/a","kind":"background","startedAt":2000,"sessionId":"s-busy","name":"alpha","status":"busy"}
	]`
	got, err := List(context.Background(), fakeRunner{out: []byte(raw)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// busy sorts first, regardless of input order.
	if got[0].Name != "alpha" || got[0].Status != "busy" {
		t.Errorf("row 0 = %q/%q, want alpha/busy", got[0].Name, got[0].Status)
	}
	if got[1].Name != "zeta" {
		t.Errorf("row 1 = %q, want zeta", got[1].Name)
	}
	// epoch-ms → time.Time.
	if !got[0].StartedAt.Equal(time.UnixMilli(2000)) {
		t.Errorf("StartedAt = %v, want %v", got[0].StartedAt, time.UnixMilli(2000))
	}
	if got[0].Kind != "background" || got[0].SessionID != "s-busy" || got[0].PID != 1 {
		t.Errorf("field mapping wrong: %+v", got[0])
	}
}

func TestList_EmptyArray_ReturnsNoSessions(t *testing.T) {
	got, err := List(context.Background(), fakeRunner{out: []byte(`[]`)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestList_RunnerError_IsUnavailable(t *testing.T) {
	_, err := List(context.Background(), fakeRunner{err: errors.New("exec: \"claude\": not found")})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestList_MalformedJSON_IsNotUnavailable(t *testing.T) {
	_, err := List(context.Background(), fakeRunner{out: []byte(`{not json`)})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("malformed JSON should be a distinct parse error, got ErrUnavailable")
	}
}
