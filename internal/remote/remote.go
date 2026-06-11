// Package remote discovers per-owner forge token scopes and fetches the
// owned repositories across every configured forge, caching the result.
package remote

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// remoteTarget is a (forge, owner) tuple discovered via an .envrc file under a
// repos root. Each target is queried independently with credentials loaded by
// `direnv exec` from that target's Dir.
type remoteTarget struct {
	Dir   string
	Forge string
	Owner string
}

// Refresh discovers every forge target reachable from roots, fetches each
// owner's repos, writes the merged result to cachePath, and returns it. The
// first per-target error is returned alongside whatever repos did succeed, so a
// single failing forge does not lose the others.
func Refresh(ctx context.Context, roots []string, cachePath string) ([]forge.RepoRef, error) {
	var targets []remoteTarget
	seen := map[string]bool{}
	for _, root := range roots {
		for _, t := range discoverRemoteTargets(root) {
			key := t.Forge + "|" + t.Owner + "|" + t.Dir
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, t)
		}
	}
	var all []forge.RepoRef
	var firstErr error
	for _, t := range targets {
		repos, err := fetchTargetRepos(ctx, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		all = append(all, repos...)
	}
	// Best-effort cache write: callers already have the fresh repos in `all`;
	// a write failure must not fail the refresh.
	_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
	return all, firstErr
}

// discoverRemoteTargets walks the well-known repos-root layout patterns and
// emits one target per .envrc-marked directory:
//
//	github/<owner>/[<public|private>/].envrc → {github, owner}
//	gitlab/<owner>/.envrc                     → {gitlab, owner}
//	git-forgejo/.envrc                        → {forgejo, freax}
//	ado/.envrc                                → {ado, ""}
//
// GitHub is the only forge that nests an extra visibility level, so its token
// .envrc may live at github/<owner>/<visibility>/.envrc; we accept either
// placement and emit a single deduped target per owner.
func discoverRemoteTargets(root string) []remoteTarget {
	var out []remoteTarget
	if d := filepath.Join(root, "github"); dirExists(d) {
		owners, _ := os.ReadDir(d)
		for _, o := range owners {
			if !o.IsDir() {
				continue
			}
			ownerDir := filepath.Join(d, o.Name())
			markerDir := ownerEnvrcDir(ownerDir)
			if markerDir != "" {
				out = append(out, remoteTarget{Dir: markerDir, Forge: "github", Owner: o.Name()})
			}
		}
	}
	if d := filepath.Join(root, "gitlab"); dirExists(d) {
		owners, _ := os.ReadDir(d)
		for _, o := range owners {
			if !o.IsDir() {
				continue
			}
			ownerDir := filepath.Join(d, o.Name())
			if fileExists(filepath.Join(ownerDir, ".envrc")) {
				out = append(out, remoteTarget{Dir: ownerDir, Forge: "gitlab", Owner: o.Name()})
			}
		}
	}
	if d := filepath.Join(root, "git-forgejo"); dirExists(d) && fileExists(filepath.Join(d, ".envrc")) {
		out = append(out, remoteTarget{Dir: d, Forge: "forgejo", Owner: "freax"})
	}
	if d := filepath.Join(root, "ado"); dirExists(d) && fileExists(filepath.Join(d, ".envrc")) {
		out = append(out, remoteTarget{Dir: d, Forge: "ado"})
	}
	return out
}

// envFromDirenv reads the named env vars under dir's direnv scope. Missing vars
// come back as empty strings. Falls back to the parent process env when direnv
// is absent or fails, so tests without direnv still resolve tokens.
func envFromDirenv(dir string, vars []string) map[string]string {
	result := make(map[string]string, len(vars))
	if _, err := exec.LookPath("direnv"); err != nil {
		for _, v := range vars {
			result[v] = os.Getenv(v)
		}
		return result
	}
	var script strings.Builder
	for _, v := range vars {
		script.WriteString(`printf '%s\n' "${`)
		script.WriteString(v)
		script.WriteString(`:-}"; `)
	}
	cmd := exec.Command("direnv", "exec", dir, "sh", "-c", script.String())
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		for _, v := range vars {
			result[v] = os.Getenv(v)
		}
		return result
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for i, v := range vars {
		if i < len(lines) {
			result[v] = lines[i]
		} else {
			result[v] = ""
		}
	}
	return result
}

func fetchTargetRepos(ctx context.Context, t remoteTarget) ([]forge.RepoRef, error) {
	switch t.Forge {
	case "github":
		env := envFromDirenv(t.Dir, []string{"GH_TOKEN", "GITHUB_TOKEN"})
		tok := env["GH_TOKEN"]
		if tok == "" {
			tok = env["GITHUB_TOKEN"]
		}
		if tok == "" {
			return nil, nil
		}
		c := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		return c.ListRepos(ctx, t.Owner)
	case "gitlab":
		env := envFromDirenv(t.Dir, []string{"GITLAB_TOKEN"})
		tok := env["GITLAB_TOKEN"]
		if tok == "" {
			return nil, nil
		}
		c := forge.NewGitlabClient(tok, os.Getenv("BRIDGE_GITLAB_API"))
		return c.ListRepos(ctx, t.Owner)
	case "forgejo":
		env := envFromDirenv(t.Dir, []string{"FORGEJO_TOKEN"})
		tok := env["FORGEJO_TOKEN"]
		if tok == "" {
			return nil, nil
		}
		c := forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
		return c.ListRepos(ctx, t.Owner)
	case "ado":
		env := envFromDirenv(t.Dir, []string{"AZURE_DEVOPS_ORG_URL", "AZURE_DEVOPS_EXT_PAT", "ADO_PAT"})
		orgURL := env["AZURE_DEVOPS_ORG_URL"]
		if api := os.Getenv("BRIDGE_ADO_API"); api != "" {
			orgURL = api
		}
		if orgURL == "" {
			return nil, nil
		}
		tok := env["AZURE_DEVOPS_EXT_PAT"]
		if tok == "" {
			tok = env["ADO_PAT"]
		}
		if tok == "" {
			return nil, nil
		}
		c := forge.NewADOClient(tok, orgURL)
		return c.ListRepos(ctx, "")
	}
	return nil, nil
}

// ownerEnvrcDir returns the directory holding the token .envrc for a GitHub
// owner: ownerDir itself when github/<owner>/.envrc exists, else the first
// immediate subdirectory carrying an .envrc (the github/<owner>/<visibility>/
// layout). Returns "" when no marker is found.
func ownerEnvrcDir(ownerDir string) string {
	if fileExists(filepath.Join(ownerDir, ".envrc")) {
		return ownerDir
	}
	subs, _ := os.ReadDir(ownerDir)
	for _, s := range subs {
		if s.IsDir() && fileExists(filepath.Join(ownerDir, s.Name(), ".envrc")) {
			return filepath.Join(ownerDir, s.Name())
		}
	}
	return ""
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
