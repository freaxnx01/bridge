package core

import (
    "testing"
)

func TestParseTmuxList(t *testing.T) {
    raw := `bridge-main|1|1716000000
ingest-bug|0|1716000100
`
    sessions, err := ParseTmuxList(raw, 1716000200)
    if err != nil {
        t.Fatal(err)
    }
    if len(sessions) != 2 {
        t.Fatalf("got %d", len(sessions))
    }
    if sessions[0].SlotID != "bridge-main" || sessions[0].State != "attached" {
        t.Errorf("[0]: %+v", sessions[0])
    }
    if sessions[1].State != "detached" {
        t.Errorf("[1]: %+v", sessions[1])
    }
    if sessions[0].Age <= 0 || sessions[1].Age <= 0 {
        t.Errorf("age: %v %v", sessions[0].Age, sessions[1].Age)
    }
}

func TestParseTmuxListEmpty(t *testing.T) {
    sessions, err := ParseTmuxList("", 0)
    if err != nil {
        t.Fatal(err)
    }
    if sessions != nil {
        t.Errorf("expected nil, got %v", sessions)
    }
}

func TestParseTmuxListBadLine(t *testing.T) {
    _, err := ParseTmuxList("only-one-field\n", 0)
    if err == nil {
        t.Error("expected error for malformed line")
    }
}
