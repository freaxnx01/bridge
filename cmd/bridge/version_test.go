package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "bridge") {
		t.Errorf("expected 'bridge' in output, got: %s", s)
	}
}
