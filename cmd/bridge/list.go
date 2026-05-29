package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/store"
)

var (
	listJSON    bool
	listRemote  bool
	listRefresh bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local repos (and optionally remote)",
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "machine-readable output")
	listCmd.Flags().BoolVarP(&listRemote, "remote", "r", false, "include remote listings")
	listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "force refresh of remote cache")
	rootCmd.AddCommand(listCmd)
}

func cacheRoot() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "bridge")
	}
	d, _ := store.Dir()
	return d
}

func runList(cmd *cobra.Command, args []string) error {
	local, err := discoverAllRoots()
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Path < local[j].Path })

	if !listRemote {
		if listJSON {
			return emitJSON(cmd.OutOrStdout(), local)
		}
		for _, r := range local {
			vis := r.Visibility
			if vis == "" {
				vis = "-"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %-8s %s\n", r.Forge, r.Owner, vis, r.Name)
		}
		return nil
	}

	remote, err := loadOrFetchRemote(cmd.Context(), local, listRefresh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: remote fetch failed, using cache: %v\n", err)
	}
	if listJSON {
		return emitJSON(cmd.OutOrStdout(), struct {
			Local  []core.Repo     `json:"local"`
			Remote []forge.RepoRef `json:"remote"`
		}{local, remote})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "# local")
	for _, r := range local {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "# remote")
	for _, r := range remote {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
	}
	return nil
}

const remoteTTL = time.Hour

// remoteTarget is a (forge, owner) tuple discovered via an .envrc file under
// reposRoot. Each target is queried independently with credentials loaded by
// `direnv exec` from that target's Dir — so an ado/.envrc scope and a
// github/<owner>/.envrc scope can both be fetched in a single refresh, even
// when the parent shell has neither loaded.
type remoteTarget struct {
	Dir   string
	Forge string
	Owner string
}

// discoverRemoteTargets walks the well-known reposRoot layout patterns and
// emits one target per .envrc-marked directory:
//
//	github/<owner>/.envrc       → {github, owner}
//	gitlab/<owner>/.envrc       → {gitlab, owner}
//	git-forgejo/.envrc          → {forgejo, freax}
//	ado/.envrc                  → {ado, ""}
//
// Owner-less ADO is intentional: ADO clones live under ado/<project>/<repo>
// where "project" is the API's project.name, not a forge-level owner.
func discoverRemoteTargets(root string) []remoteTarget {
	var out []remoteTarget
	if d := filepath.Join(root, "github"); dirExists(d) {
		owners, _ := os.ReadDir(d)
		for _, o := range owners {
			if !o.IsDir() {
				continue
			}
			ownerDir := filepath.Join(d, o.Name())
			if fileExists(filepath.Join(ownerDir, ".envrc")) {
				out = append(out, remoteTarget{Dir: ownerDir, Forge: "github", Owner: o.Name()})
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

// envFromDirenv reads the named env vars under dir's direnv scope. Missing
// vars come back as empty strings. Falls back to the parent process env when
// direnv is absent or fails, so tests without direnv still resolve tokens.
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

func loadOrFetchRemote(ctx context.Context, local []core.Repo, refresh bool) ([]forge.RepoRef, error) {
	cachePath := filepath.Join(cacheRoot(), "remote.list")
	if !refresh {
		c, err := forge.ReadRepoCache(cachePath)
		if err == nil && !c.IsStale(remoteTTL) && len(c.Repos) > 0 {
			return c.Repos, nil
		}
	}
	var targets []remoteTarget
	seenTargets := map[string]bool{}
	for _, root := range reposRoots() {
		for _, t := range discoverRemoteTargets(root) {
			key := t.Forge + "|" + t.Owner + "|" + t.Dir
			if seenTargets[key] {
				continue
			}
			seenTargets[key] = true
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
	_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
	return all, firstErr
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

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
