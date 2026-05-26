package shellbridge

import (
	"bytes"
	"testing"
)

func TestDirectiveCD(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitCD(&buf, "/home/me/projects/repos/github/me/public/bridge"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "cd:/home/me/projects/repos/github/me/public/bridge\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDirectiveExec(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitExec(&buf, []string{"tmux", "new-session", "-A", "-s", "slot-x"}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "exec:tmux new-session -A -s slot-x\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDirectiveNoop(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitNoop(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "noop\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestDirectiveExecRejectsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitExec(&buf, nil); err == nil {
		t.Error("expected error on empty argv")
	}
}

func TestDirectiveExecQuotesArgsWithSpaces(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitExec(&buf, []string{"sh", "-c", "echo hi there"}); err != nil {
		t.Fatal(err)
	}
	want := "exec:sh -c 'echo hi there'\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestDirectiveExecQuotesArgsWithSingleQuote(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitExec(&buf, []string{"echo", "it's me"}); err != nil {
		t.Fatal(err)
	}
	want := "exec:echo 'it'\\''s me'\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}
