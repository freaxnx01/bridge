package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// PickerChoice is a discriminated union of local-repo / remote-only results
// from the combined picker. Exactly one of Local / Remote is non-nil for a
// successful pick. Both nil = cancelled (also signaled by the ok=false
// return from pickRepoOrRemote).
type PickerChoice struct {
	Local  *core.Repo
	Remote *forge.RepoRef
}

// entryLabel returns the display label for a picker row:
//
//	github  → github/public/name  or  github/private/name
//	ado     → ado/ProjectName/name
//	gitlab  → gitlab/owner/name
//	forgejo → forgejo/name
func entryLabel(forge, owner, vis, name string) string {
	switch forge {
	case "github":
		v := vis
		if v == "" {
			v = "-"
		}
		return forge + "/" + v + "/" + name
	case "forgejo":
		return forge + "/" + name
	default:
		if owner != "" {
			return forge + "/" + owner + "/" + name
		}
		return forge + "/" + name
	}
}

func localEntryLabel(r core.Repo) string {
	return entryLabel(r.Forge, r.Owner, r.Visibility, r.Name)
}

func remoteEntryLabel(r forge.RepoRef) string {
	return entryLabel(r.Forge, r.Owner, r.Visibility, r.Name)
}

// pickRepoOrRemote runs an fzf picker over local repos plus remote-only refs.
// Remote rows are prefixed with "↓ " (bash bridge convention) so they're
// visually distinct. Remote entries whose forge+owner+name matches a local
// repo are dropped (the local entry covers it).
//
// Test hooks (shared with pickRepo):
//
//	BRIDGE_PICKER_FIXTURE — match against local Repo.Name first, then remote RepoRef.Name.
//	BRIDGE_PICKER_FIXTURE_CANCEL — return cancel.
func pickRepoOrRemote(local []core.Repo, remote []forge.RepoRef) (PickerChoice, bool, error) {
	remote = filterRemoteOnly(local, remote)

	if os.Getenv("BRIDGE_PICKER_FIXTURE_CANCEL") != "" {
		return PickerChoice{}, false, nil
	}
	if name := os.Getenv("BRIDGE_PICKER_FIXTURE"); name != "" {
		if r, ok := findRepoByName(local, name); ok {
			return PickerChoice{Local: &r}, true, nil
		}
		needle := strings.ToLower(name)
		for i := range remote {
			if strings.ToLower(remote[i].Name) == needle {
				return PickerChoice{Remote: &remote[i]}, true, nil
			}
		}
		return PickerChoice{}, false, nil
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return PickerChoice{}, false, errors.New("fzf not found in PATH; install fzf to use the picker")
	}

	// Build all rows, then sort by forge/vis/name so entries group naturally.
	// Format: <display>\t<kind>\t<key>
	// kind ∈ {local, remote}; key is Path (local) or forge|owner|name (remote).
	type row struct {
		display string
		sortKey string
		kind    string
		key     string
	}
	var rows []row
	for _, r := range local {
		lbl := localEntryLabel(r)
		rows = append(rows, row{lbl, strings.ToLower(lbl), "local", r.Path})
	}
	for _, r := range remote {
		lbl := remoteEntryLabel(r)
		rows = append(rows, row{"↓ " + lbl, strings.ToLower(lbl), "remote", r.Forge + "|" + r.Owner + "|" + r.Name})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].sortKey != rows[j].sortKey {
			return rows[i].sortKey < rows[j].sortKey
		}
		return rows[i].kind == "local" // local before remote on ties
	})
	var input bytes.Buffer
	for _, r := range rows {
		fmt.Fprintf(&input, "%s\t%s\t%s\n", r.display, r.kind, r.key)
	}
	cmd := exec.Command("fzf", "--with-nth=1", "--delimiter=\t", "--prompt=bridge> ", "--ansi", "--layout=reverse")
	cmd.Stdin = &input
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return PickerChoice{}, false, nil
		}
		return PickerChoice{}, false, err
	}
	chosen := strings.TrimSpace(out.String())
	if chosen == "" {
		return PickerChoice{}, false, nil
	}
	parts := strings.SplitN(chosen, "\t", 3)
	if len(parts) != 3 {
		return PickerChoice{}, false, errors.New("picker: malformed selection")
	}
	switch parts[1] {
	case "local":
		for i := range local {
			if local[i].Path == parts[2] {
				return PickerChoice{Local: &local[i]}, true, nil
			}
		}
	case "remote":
		k := strings.SplitN(parts[2], "|", 3)
		if len(k) != 3 {
			return PickerChoice{}, false, errors.New("picker: malformed remote key")
		}
		for i := range remote {
			if remote[i].Forge == k[0] && remote[i].Owner == k[1] && remote[i].Name == k[2] {
				return PickerChoice{Remote: &remote[i]}, true, nil
			}
		}
	}
	return PickerChoice{}, false, errors.New("picker: chosen entry not in list")
}

// filterRemoteOnly drops remote refs whose forge+owner+name matches a local
// repo. Comparison is case-sensitive on forge/owner (path components) and
// case-insensitive on name (filesystems aren't reliably case-sensitive).
func filterRemoteOnly(local []core.Repo, remote []forge.RepoRef) []forge.RepoRef {
	have := map[string]bool{}
	for _, r := range local {
		have[r.Forge+"/"+r.Owner+"/"+strings.ToLower(r.Name)] = true
	}
	out := make([]forge.RepoRef, 0, len(remote))
	for _, r := range remote {
		k := r.Forge + "/" + r.Owner + "/" + strings.ToLower(r.Name)
		if have[k] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// readRemoteCache returns whatever is currently in remote.list regardless of
// staleness. Empty / missing cache → empty slice, no error. Used by the
// picker path so bare `-r` can show stale remote rows without forcing a
// network call.
func readRemoteCache() []forge.RepoRef {
	c, err := forge.ReadRepoCache(filepath.Join(cacheRoot(), "remote.list"))
	if err != nil {
		return nil
	}
	return c.Repos
}

// cloneRemoteRepo runs `direnv exec <parent_dir> git clone <url> <target>`
// where parent_dir is the closest dir under reposRoot containing an .envrc
// (so direnv loads the right forge token). Returns the cloned repo's path.
//
// Clone progress streams to stderr (visible to the user). On failure the
// target dir is removed so a retry can succeed.
func cloneRemoteRepo(ref forge.RepoRef) (string, error) {
	if _, err := exec.LookPath("direnv"); err != nil {
		return "", fmt.Errorf("clone: direnv not found in PATH (required for credential loading): %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("clone: git not found in PATH: %w", err)
	}

	parentDir, targetDir, err := remoteCloneDirs(reposRoot(), ref)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("clone: mkdir parent: %w", err)
	}
	url := cloneURLFor(ref)
	if url == "" {
		return "", fmt.Errorf("clone: no usable URL for %s/%s/%s (HTMLURL+SSHURL both empty)", ref.Forge, ref.Owner, ref.Name)
	}

	fmt.Fprintf(os.Stderr, "bridge: cloning %s → %s\n", url, targetDir)
	cmd := exec.Command("direnv", "exec", parentDir, "git", "clone", url, targetDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Best-effort cleanup so retry isn't blocked by a half-clone.
		_ = os.RemoveAll(targetDir)
		return "", fmt.Errorf("clone: %w", err)
	}
	return targetDir, nil
}

// remoteCloneDirs derives (parent_dir, target_dir) for a remote ref under
// reposRoot, following the path-pattern layout. Errors on unknown forge.
//
//	github/<owner>/(public|private)/<repo>   parent = github/<owner>/<vis>
//	gitlab/<owner>/<repo>                    parent = gitlab/<owner>
//	git-forgejo/<repo>                       parent = git-forgejo
func remoteCloneDirs(reposRoot string, ref forge.RepoRef) (string, string, error) {
	switch ref.Forge {
	case "github":
		vis := ref.Visibility
		if vis != "public" && vis != "private" {
			// RepoRef.Visibility from forge clients is "public" / "private" /
			// "internal". Map "internal" → "private" since we don't have an
			// internal/ subdir layout.
			vis = "private"
		}
		parent := filepath.Join(reposRoot, "github", ref.Owner, vis)
		return parent, filepath.Join(parent, ref.Name), nil
	case "gitlab":
		parent := filepath.Join(reposRoot, "gitlab", ref.Owner)
		return parent, filepath.Join(parent, ref.Name), nil
	case "forgejo":
		parent := filepath.Join(reposRoot, "git-forgejo")
		return parent, filepath.Join(parent, ref.Name), nil
	case "ado":
		// Owner = ADO project name; clones under ado/<project>/<repo>
		parent := filepath.Join(reposRoot, "ado", ref.Owner)
		return parent, filepath.Join(parent, ref.Name), nil
	}
	return "", "", fmt.Errorf("unknown forge %q", ref.Forge)
}

// cloneURLFor picks the right clone URL per forge: GitHub/GitLab use HTTPS
// (auth via direnv-loaded credential helper); Forgejo uses SSH.
func cloneURLFor(ref forge.RepoRef) string {
	switch ref.Forge {
	case "github", "gitlab":
		if ref.HTMLURL != "" {
			return ref.HTMLURL
		}
		return ref.SSHURL
	case "forgejo":
		if ref.SSHURL != "" {
			return ref.SSHURL
		}
		return ref.HTMLURL
	case "ado":
		if ref.HTMLURL != "" {
			return ref.HTMLURL
		}
		return ref.SSHURL
	}
	return ref.HTMLURL
}

// Convenience: after cloning, rebuild a core.Repo describing the new local
// path so the rest of the launch flow (MRU touch, agent launch) is identical.
func repoFromClonedRef(reposRoot string, ref forge.RepoRef, targetDir string) core.Repo {
	vis := ref.Visibility
	if ref.Forge != "github" {
		vis = ""
	}
	return core.Repo{
		Name:       ref.Name,
		Path:       targetDir,
		Forge:      ref.Forge,
		Owner:      ref.Owner,
		Visibility: vis,
	}
}

