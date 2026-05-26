package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPreflightNoArgs(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "__preflight").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s != "noop" {
		t.Errorf("got %q, want noop", s)
	}
}

func TestPreflightUnknownVerb(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "__preflight", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "noop" {
		t.Errorf("got %q", out)
	}
}

func TestPreflightIsHidden(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "__preflight") {
		t.Errorf("__preflight should be hidden from --help, got:\n%s", out)
	}
}
