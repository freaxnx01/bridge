package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/capture"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/remote"
)

var captureTarget string

var captureCmd = &cobra.Command{
	Use:   "capture",
	Short: "Capture ideas/issues/roadmap items into Git-backed destinations",
}

var captureIdeaCmd = &cobra.Command{
	Use:   "idea",
	Short: "Capture an idea (text from stdin) to a repo's ideas.md or ideas-lab",
	RunE:  runCaptureIdea,
}

func init() {
	captureIdeaCmd.Flags().StringVar(&captureTarget, "target", "", "ideas-lab | <repo-name> | <owner>/<name>")
	captureIdeaCmd.MarkFlagRequired("target")
	captureCmd.AddCommand(captureIdeaCmd)
	rootCmd.AddCommand(captureCmd)
}

// issueTarget is what resolveIssueTarget returns: identifies the repo AND its
// forge (which we need to pick the right client + token).
type issueTarget struct {
	Owner, Repo, Forge string
}

// resolveIssueTarget maps a --target string ("owner/name" or a bare repo name)
// to an issueTarget by looking it up in the discovered repos. Unlike the
// idea-capture resolver, this one accepts both github and forgejo repos and
// rejects the "ideas-lab" sentinel.
func resolveIssueTarget(target string, repos []core.Repo) (issueTarget, error) {
	if target == "ideas-lab" {
		return issueTarget{}, fmt.Errorf("ideas-lab target is for ideas only; pick a repo")
	}
	if owner, name, ok := strings.Cut(target, "/"); ok {
		for i := range repos {
			if strings.EqualFold(repos[i].Owner, owner) && strings.EqualFold(repos[i].Name, name) {
				return issueTarget{Owner: repos[i].Owner, Repo: repos[i].Name, Forge: repos[i].Forge}, nil
			}
		}
		return issueTarget{}, fmt.Errorf("no known repo %s/%s (need its forge to create the issue)", owner, name)
	}
	var match *core.Repo
	for i := range repos {
		f := repos[i].Forge
		if (f == "github" || f == "forgejo") && strings.EqualFold(repos[i].Name, target) {
			if match != nil {
				return issueTarget{}, fmt.Errorf("repo %q is ambiguous; use owner/name", target)
			}
			match = &repos[i]
		}
	}
	if match == nil {
		return issueTarget{}, fmt.Errorf("no github/forgejo repo named %q", target)
	}
	return issueTarget{Owner: match.Owner, Repo: match.Name, Forge: match.Forge}, nil
}

var captureIssueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Capture an issue (title from stdin) on a chosen repo",
	RunE:  runCaptureIssue,
}

var captureIssueTarget string

func init() {
	captureIssueCmd.Flags().StringVar(&captureIssueTarget, "target", "", "<repo-name> | <owner>/<name>")
	_ = captureIssueCmd.MarkFlagRequired("target")
	captureCmd.AddCommand(captureIssueCmd)
}

func runCaptureIssue(cmd *cobra.Command, args []string) error {
	repos, _ := discoverAllRoots()
	tgt, err := resolveIssueTarget(captureIssueTarget, repos)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read title: %w", err)
	}
	title := firstNonEmptyLine(string(raw))
	if title == "" {
		return fmt.Errorf("no title on stdin")
	}
	var creator capture.IssueCreator
	switch tgt.Forge {
	case "github":
		tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
		if !ok {
			return fmt.Errorf("no github token for owner %q (need an .envrc GH_TOKEN with repo scope)", tgt.Owner)
		}
		creator = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
	case "forgejo":
		tok, ok := remote.ForgejoToken(reposRoots())
		if !ok {
			return fmt.Errorf("no forgejo token (need a git-forgejo .envrc with FORGEJO_TOKEN)")
		}
		creator = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
	default:
		return fmt.Errorf("forge %q is not supported for issue capture", tgt.Forge)
	}
	is, err := capture.CaptureIssue(cmd.Context(), creator, tgt.Owner, tgt.Repo, title)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), is.URL)
	return nil
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// resolveCaptureTarget maps a --target string to a capture.Target. "ideas-lab"
// uses ideasLabRepo ("owner/name", from config). Otherwise the string is a repo
// identifier: "owner/name" is taken literally; a bare name is matched (case-
// insensitively) against the discovered repos (error on none/ambiguous).
func resolveCaptureTarget(target, ideasLabRepo string, repos []core.Repo) (capture.Target, error) {
	if target == "ideas-lab" {
		owner, name, ok := strings.Cut(ideasLabRepo, "/")
		if !ok || owner == "" || name == "" {
			return capture.Target{}, fmt.Errorf("ideas-lab target requires BRIDGE_IDEAS_LAB_REPO=owner/name")
		}
		return capture.Target{IdeasLab: true, Owner: owner, Repo: name}, nil
	}
	if owner, name, ok := strings.Cut(target, "/"); ok {
		return capture.Target{Owner: owner, Repo: name}, nil
	}
	var match *core.Repo
	for i := range repos {
		if strings.EqualFold(repos[i].Name, target) && repos[i].Forge == "github" {
			if match != nil {
				return capture.Target{}, fmt.Errorf("repo %q is ambiguous; use owner/name", target)
			}
			match = &repos[i]
		}
	}
	if match == nil {
		return capture.Target{}, fmt.Errorf("no github repo named %q", target)
	}
	return capture.Target{Owner: match.Owner, Repo: match.Name}, nil
}

func runCaptureIdea(cmd *cobra.Command, args []string) error {
	repos, _ := discoverAllRoots()
	tgt, err := resolveCaptureTarget(captureTarget, os.Getenv("BRIDGE_IDEAS_LAB_REPO"), repos)
	if err != nil {
		return err
	}
	textBytes, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read idea text: %w", err)
	}
	text := strings.TrimSpace(string(textBytes))
	if text == "" {
		return fmt.Errorf("no idea text on stdin")
	}
	tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
	if !ok {
		return fmt.Errorf("no github token for owner %q (need an .envrc GH_TOKEN with repo scope)", tgt.Owner)
	}
	client := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
	url, err := capture.CaptureIdea(context.Background(), client, tgt, text, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), url)
	return nil
}
