package agents

import "testing"

func TestResolveClaude(t *testing.T) {
	s, err := Resolve("claude")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "claude" || s.Bin != "claude" || len(s.Args) != 0 {
		t.Errorf("%+v", s)
	}
}

func TestResolveCopilot(t *testing.T) {
	s, err := Resolve("copilot")
	if err != nil {
		t.Fatal(err)
	}
	if s.Bin != "copilot" {
		t.Errorf("%+v", s)
	}
}

func TestResolveOpencode(t *testing.T) {
	s, err := Resolve("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if s.Bin != "opencode" {
		t.Errorf("%+v", s)
	}
}

func TestResolveCode(t *testing.T) {
	s, err := Resolve("code")
	if err != nil {
		t.Fatal(err)
	}
	if s.Bin != "code" || len(s.Args) != 1 || s.Args[0] != "." {
		t.Errorf("%+v", s)
	}
}

func TestResolveUnknown(t *testing.T) {
	if _, err := Resolve("bogus"); err == nil {
		t.Error("expected error")
	}
}

func TestResolveDefault(t *testing.T) {
	s := Default()
	if s.Name != "claude" {
		t.Errorf("default should be claude, got %s", s.Name)
	}
}
