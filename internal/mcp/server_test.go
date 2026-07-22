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

func TestNewServer_RegistersExpectedToolSet(t *testing.T) {
	names := advertisedTools(t, Deps{})
	want := []string{"add_labels", "close_issue", "comment_issue", "create_issue", "create_repo", "cross_forge_status", "list_git_forges", "list_issues", "list_repos", "read_file", "update_issue"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
}

// TestListGitForges_AdvertisesOnlyRegisteredTools asserts the real invariant
// behind the read/normal-mode capability tests above: every tool name
// list_git_forges advertises in a forge's capabilities must be a tool the
// server actually registered. A future write tool added to Capabilities but
// forgotten in isWriteTool would previously only be caught by a count
// mismatch (want 3, got 4) that doesn't name the offending tool; this fails
// on the specific name instead.
func TestListGitForges_AdvertisesOnlyRegisteredTools(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		name := "normal"
		if readOnly {
			name = "read-only"
		}
		t.Run(name, func(t *testing.T) {
			d := Deps{
				ReadOnly:      readOnly,
				DefaultOwners: []Target{{Forge: "github", Owner: "o"}},
				ClientFor:     func(string, string) ForgeReader { return newFakeFull("github") },
			}
			registered := advertisedTools(t, d)
			registeredSet := make(map[string]bool, len(registered))
			for _, n := range registered {
				registeredSet[n] = true
			}

			_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range out.Forges[0].Capabilities {
				if !registeredSet[c] {
					t.Errorf("list_git_forges advertised %q, but the server never registered it (registered: %v)", c, registered)
				}
			}
		})
	}
}

func TestNewServer_ReadOnlyOmitsBothWriteTools(t *testing.T) {
	names := advertisedTools(t, Deps{ReadOnly: true})
	want := []string{"cross_forge_status", "list_git_forges", "list_issues", "list_repos", "read_file"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
	for _, n := range names {
		if n == "create_issue" || n == "create_repo" {
			t.Fatalf("read-only server must not advertise write tools: %v", names)
		}
	}
}
