package core

import (
    "testing"
    "time"
)

func TestParseTmuxList(t *testing.T) {
    raw := `bridge-main|1|1716000000|1716000150
ingest-bug|0|1716000100|1716000180
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

func TestParseTmuxList_FourFields_PopulatesLastActivity(t *testing.T) {
	// name|attached|created|activity
	raw := "fix-x|1|1000|1900\ndocs|0|1000|1500\n"
	got, err := ParseTmuxList(raw, 2000)
	if err != nil {
		t.Fatalf("ParseTmuxList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].State != "attached" {
		t.Errorf("session[0].State = %q, want attached", got[0].State)
	}
	if want := time.Unix(1900, 0); !got[0].LastActivity.Equal(want) {
		t.Errorf("session[0].LastActivity = %v, want %v", got[0].LastActivity, want)
	}
	if want := time.Unix(1500, 0); !got[1].LastActivity.Equal(want) {
		t.Errorf("session[1].LastActivity = %v, want %v", got[1].LastActivity, want)
	}
}

func TestParseTmuxList_EmptyActivity_FallsBackToCreated(t *testing.T) {
	got, err := ParseTmuxList("x|1|1000|\n", 2000)
	if err != nil {
		t.Fatalf("ParseTmuxList should tolerate empty activity: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	if want := time.Unix(1000, 0); !got[0].LastActivity.Equal(want) {
		t.Errorf("LastActivity = %v, want fallback to created %v", got[0].LastActivity, want)
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
