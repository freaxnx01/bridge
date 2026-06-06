package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/forge"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validRepoName(s string) bool {
	return s != ".." && s != "." && repoNameRe.MatchString(s)
}

// cloneFn is a seam so tests can stub the actual git clone.
var cloneFn = func(sshURL, target string) error {
	c := exec.Command("git", "clone", sshURL, target)
	// clone progress on stderr, keep stdout clean for --json
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

const githubOwner = "freaxnx01"

func init() {
	rootCmd.AddCommand(newCreateCmd())
}

func newCreateCmd() *cobra.Command {
	var forgeName string
	var public, asJSON bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new repo (Forgejo or GitHub) and clone it locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args[0], forgeName, public, asJSON)
		},
	}
	cmd.Flags().StringVar(&forgeName, "forge", "forgejo", "forge: forgejo|github")
	cmd.Flags().BoolVar(&public, "public", false, "create a public repo (default private)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func forgejoTargetDir() (dir, token string, err error) {
	for _, root := range reposRoots() {
		d := filepath.Join(root, "git-forgejo")
		if dirExists(d) {
			tok := envFromDirenv(d, []string{"FORGEJO_TOKEN"})["FORGEJO_TOKEN"]
			if tok == "" {
				tok = os.Getenv("FORGEJO_TOKEN")
			}
			return d, tok, nil
		}
	}
	return "", "", fmt.Errorf("no git-forgejo dir under repos roots")
}

func githubTargetDir(vis string) (dir, token string, err error) {
	for _, root := range reposRoots() {
		d := filepath.Join(root, "github", githubOwner, vis)
		if dirExists(d) {
			env := envFromDirenv(d, []string{"GH_TOKEN", "GITHUB_TOKEN"})
			tok := env["GH_TOKEN"]
			if tok == "" {
				tok = env["GITHUB_TOKEN"]
			}
			// direnv strips some env vars (e.g. GH_TOKEN) for security when
			// no .envrc exports them; fall back to the process env so tests
			// using t.Setenv and integration setups without direnv still work.
			if tok == "" {
				tok = os.Getenv("GH_TOKEN")
			}
			if tok == "" {
				tok = os.Getenv("GITHUB_TOKEN")
			}
			return d, tok, nil
		}
	}
	return "", "", fmt.Errorf("no github/%s/%s dir under repos roots", githubOwner, vis)
}

func runCreate(cmd *cobra.Command, name, forgeName string, public, asJSON bool) error {
	if !validRepoName(name) {
		return fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", name)
	}
	private := !public
	vis := "private"
	if public {
		vis = "public"
	}
	ctx := context.Background()

	var ref forge.RepoRef
	var targetDir string
	switch forgeName {
	case "forgejo":
		dir, tok, err := forgejoTargetDir()
		if err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("no Forgejo token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	case "github":
		dir, tok, err := githubTargetDir(vis)
		if err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("no GitHub token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	default:
		return fmt.Errorf("unknown forge %q (use forgejo or github)", forgeName)
	}

	if err := cloneFn(ref.SSHURL, targetDir); err != nil {
		return fmt.Errorf("created %s but clone failed: %v\nclone manually: git clone %s %s",
			ref.HTMLURL, err, ref.SSHURL, targetDir)
	}

	if asJSON {
		return emitJSON(cmd.OutOrStdout(), map[string]any{
			"name": ref.Name, "full_name": ref.Owner + "/" + ref.Name,
			"forge": forgeName, "private": private, "path": targetDir, "html_url": ref.HTMLURL,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created %s/%s (%s, %s) -> %s\n",
		ref.Owner, ref.Name, vis, forgeName, targetDir)
	return nil
}

func createErr(err error, name string) error {
	if errors.Is(err, forge.ErrRepoExists) {
		return fmt.Errorf("repo %q already exists", name)
	}
	return err
}
