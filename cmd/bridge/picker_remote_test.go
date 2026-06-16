package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestFilterRemoteOnlyDropsMatches(t *testing.T) {
	local := []core.Repo{
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
		{Forge: "gitlab", Owner: "me", Name: "foo"},
	}
	remote := []forge.RepoRef{
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"}, // dropped
		{Forge: "github", Owner: "freaxnx01", Name: "newrepo"},
		{Forge: "github", Owner: "OTHER", Name: "bridge"}, // kept (different owner)
		{Forge: "gitlab", Owner: "me", Name: "foo"},       // dropped
		{Forge: "gitlab", Owner: "me", Name: "Bar"},       // kept
	}
	got := filterRemoteOnly(local, remote)
	wantNames := []string{"newrepo", "bridge", "Bar"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(wantNames), got)
	}
	for i, w := range wantNames {
		if got[i].Name != w {
			t.Errorf("[%d] got %q want %q", i, got[i].Name, w)
		}
	}
}

func TestFilterRemoteOnlyCaseInsensitiveOnName(t *testing.T) {
	local := []core.Repo{{Forge: "github", Owner: "me", Name: "Bridge"}}
	remote := []forge.RepoRef{{Forge: "github", Owner: "me", Name: "bridge"}}
	got := filterRemoteOnly(local, remote)
	if len(got) != 0 {
		t.Errorf("case-insensitive name match failed: %+v", got)
	}
}

func TestFilterRemoteOnlyCaseInsensitiveOnOwner(t *testing.T) {
	// The local owner is derived from the on-disk path while the remote owner
	// comes from the forge API, so their casing can differ for the same repo.
	local := []core.Repo{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
	remote := []forge.RepoRef{{Forge: "github", Owner: "FreaxNx01", Name: "bridge"}}
	got := filterRemoteOnly(local, remote)
	if len(got) != 0 {
		t.Errorf("case-insensitive owner match failed: remote should be deduped, got %+v", got)
	}
}

func TestRemoteCloneDirsGithubPublic(t *testing.T) {
	parent, target, err := remoteCloneDirs("/r", forge.RepoRef{Forge: "github", Owner: "me", Name: "bridge", Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if parent != "/r/github/me/public" || target != "/r/github/me/public/bridge" {
		t.Errorf("got parent=%q target=%q", parent, target)
	}
}

func TestRemoteCloneDirsGithubInternalMapsToPrivate(t *testing.T) {
	_, target, err := remoteCloneDirs("/r", forge.RepoRef{Forge: "github", Owner: "me", Name: "x", Visibility: "internal"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(target, "/github/me/private/x") {
		t.Errorf("internal should map to private: %q", target)
	}
}

func TestRemoteCloneDirsGitlab(t *testing.T) {
	parent, target, err := remoteCloneDirs("/r", forge.RepoRef{Forge: "gitlab", Owner: "g", Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if parent != "/r/gitlab/g" || target != "/r/gitlab/g/p" {
		t.Errorf("got parent=%q target=%q", parent, target)
	}
}

func TestRemoteCloneDirsForgejo(t *testing.T) {
	parent, target, err := remoteCloneDirs("/r", forge.RepoRef{Forge: "forgejo", Owner: "freax", Name: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if parent != "/r/git-forgejo" || target != "/r/git-forgejo/repo" {
		t.Errorf("got parent=%q target=%q", parent, target)
	}
}

func TestRemoteCloneDirsUnknownForgeErrors(t *testing.T) {
	if _, _, err := remoteCloneDirs("/r", forge.RepoRef{Forge: "bitbucket", Name: "x"}); err == nil {
		t.Error("expected error for unknown forge")
	}
}

func TestDirHasContentsMissingDir(t *testing.T) {
	got, err := dirHasContents(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("missing dir should report no contents")
	}
}

func TestDirHasContentsEmptyDir(t *testing.T) {
	got, err := dirHasContents(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("empty dir should report no contents")
	}
}

func TestDirHasContentsNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := dirHasContents(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("non-empty dir should report contents")
	}
}

func TestCloneURLForGithubHTTPS(t *testing.T) {
	url := cloneURLFor(forge.RepoRef{Forge: "github", HTMLURL: "https://github.com/me/x", SSHURL: "git@github.com:me/x.git"})
	if url != "https://github.com/me/x" {
		t.Errorf("github should prefer HTTPS, got %q", url)
	}
}

func TestCloneURLForForgejoSSH(t *testing.T) {
	url := cloneURLFor(forge.RepoRef{Forge: "forgejo", HTMLURL: "https://forgejo/me/x", SSHURL: "git@forgejo:me/x.git"})
	if url != "git@forgejo:me/x.git" {
		t.Errorf("forgejo should prefer SSH, got %q", url)
	}
}

func TestEntrySortKeyPrivateBeforePublic(t *testing.T) {
	priv := entrySortKey("github", "me", "private", "zzz")
	pub := entrySortKey("github", "me", "public", "aaa")
	if !(priv < pub) {
		t.Errorf("private should sort before public regardless of name: priv=%q pub=%q", priv, pub)
	}
}

func TestEntrySortKeyForgeAscending(t *testing.T) {
	ado := entrySortKey("ado", "Proj", "", "z")
	gh := entrySortKey("github", "me", "private", "a")
	if !(ado < gh) {
		t.Errorf("ado should sort before github: ado=%q gh=%q", ado, gh)
	}
}

func TestEntryLabelGithubVisInPath(t *testing.T) {
	if got := entryLabel("github", "me", "private", "bridge"); got != "github/private/bridge" {
		t.Errorf("github private label: %q", got)
	}
	if got := entryLabel("github", "me", "public", "bridge"); got != "github/public/bridge" {
		t.Errorf("github public label: %q", got)
	}
	if got := entryLabel("ado", "Proj", "", "Repo"); got != "ado/Proj/Repo" {
		t.Errorf("ado label: %q", got)
	}
	if got := entryLabel("forgejo", "freax", "", "site"); got != "forgejo/site" {
		t.Errorf("forgejo label: %q", got)
	}
}

// Two different owners with the same github repo name + visibility base-render
// to the same label (github/public/ai-instructions) and look like a duplicate.
// They are different repos, so the owner is injected to disambiguate; a
// uniquely-named repo keeps its clean owner-less label.
func TestPickerLabel_SameNameDifferentOwners_OwnerQualified(t *testing.T) {
	local := []core.Repo{
		{Forge: "github", Owner: "freaxnx01", Name: "ai-instructions", Visibility: "public"},
		{Forge: "github", Owner: "freaxnx01", Name: "bridge", Visibility: "public"},
	}
	remote := []forge.RepoRef{
		{Forge: "github", Owner: "anim-bossinfo-ch", Name: "ai-instructions", Visibility: "public"},
	}
	collide := collidingLabels(local, remote)

	tests := []struct {
		name, forge, owner, vis, repo, want string
	}{
		{"local colliding", "github", "freaxnx01", "public", "ai-instructions", "github/public/freaxnx01/ai-instructions"},
		{"remote colliding", "github", "anim-bossinfo-ch", "public", "ai-instructions", "github/public/anim-bossinfo-ch/ai-instructions"},
		{"unique name keeps clean label", "github", "freaxnx01", "public", "bridge", "github/public/bridge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickerLabel(collide, tt.forge, tt.owner, tt.vis, tt.repo); got != tt.want {
				t.Errorf("pickerLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsDirenvBlocked(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"blocked message", "direnv: error /x/.envrc is blocked. Run `direnv allow` to approve its content", true},
		{"normal loading", "direnv: loading ~/x/.envrc", false},
		{"empty", "", false},
		{"unrelated error", "direnv: error: command not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDirenvBlocked(tt.stderr); got != tt.want {
				t.Errorf("isDirenvBlocked(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestRepoFromClonedRef(t *testing.T) {
	ref := forge.RepoRef{Forge: "github", Owner: "me", Name: "bridge", Visibility: "public"}
	got := repoFromClonedRef("/r", ref, "/r/github/me/public/bridge")
	want := core.Repo{Name: "bridge", Path: "/r/github/me/public/bridge", Forge: "github", Owner: "me", Visibility: "public"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}
