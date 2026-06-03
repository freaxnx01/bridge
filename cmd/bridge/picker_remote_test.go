package main

import (
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

func TestRepoFromClonedRef(t *testing.T) {
	ref := forge.RepoRef{Forge: "github", Owner: "me", Name: "bridge", Visibility: "public"}
	got := repoFromClonedRef("/r", ref, "/r/github/me/public/bridge")
	want := core.Repo{Name: "bridge", Path: "/r/github/me/public/bridge", Forge: "github", Owner: "me", Visibility: "public"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}
