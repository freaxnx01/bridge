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
