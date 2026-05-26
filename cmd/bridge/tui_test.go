package main

import "testing"

func TestTUINotImplemented(t *testing.T) {
	cmd := bridgeCmd("tui")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
}
