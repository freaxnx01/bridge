package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func advertisedTools(t *testing.T, deps Deps) []string {
	t.Helper()
	ctx := context.Background()
	srv := NewServer(deps)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func TestNewServer_RegistersFourToolsByDefault(t *testing.T) {
	names := advertisedTools(t, Deps{})
	want := []string{"create_issue", "cross_forge_status", "list_repos", "read_file"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
}

func TestNewServer_ReadOnlyOmitsCreateIssue(t *testing.T) {
	names := advertisedTools(t, Deps{ReadOnly: true})
	for _, n := range names {
		if n == "create_issue" {
			t.Fatalf("read-only server must not advertise create_issue: %v", names)
		}
	}
	if len(names) != 3 {
		t.Fatalf("want 3 tools in read-only mode, got %v", names)
	}
}
