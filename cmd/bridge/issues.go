package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/forge"
)

var (
	issuesJSON    bool
	issuesRefresh bool
)

var issuesCmd = &cobra.Command{
	Use:   "issues",
	Short: "List open issues across forges (cached)",
	RunE:  runIssues,
}

func init() {
	issuesCmd.Flags().BoolVar(&issuesJSON, "json", false, "machine-readable output")
	issuesCmd.Flags().BoolVar(&issuesRefresh, "refresh", false, "force refresh of issues cache")
	rootCmd.AddCommand(issuesCmd)
}

const issuesTTL = 10 * time.Minute

func runIssues(cmd *cobra.Command, args []string) error {
	cachePath := filepath.Join(cacheRoot(), "issues.json")
	if !issuesRefresh {
		c, err := forge.ReadIssueCache(cachePath)
		if err == nil && !c.IsStale(issuesTTL) && len(c.Issues) > 0 {
			return renderIssues(cmd, c.Issues)
		}
	}
	repos, err := discoverAllRoots()
	if err != nil {
		return err
	}
	var all []forge.Issue
	var firstErr error
	ctx := context.Background()
	for _, r := range repos {
		client := clientFor(r.Forge)
		if client == nil {
			continue
		}
		issues, err := client.ListOpenIssues(ctx, r.Owner, r.Name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		all = append(all, issues...)
	}
	_ = forge.WriteIssueCache(cachePath, forge.IssueCache{UpdatedAt: time.Now(), Issues: all})
	if err := renderIssues(cmd, all); err != nil {
		return err
	}
	return firstErr
}

func renderIssues(cmd *cobra.Command, issues []forge.Issue) error {
	if issuesJSON {
		return emitJSON(cmd.OutOrStdout(), issues)
	}
	for _, i := range issues {
		fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-30s #%-5d %s\n", i.Forge, i.Repo, i.Number, i.Title)
	}
	return nil
}

func clientFor(name string) forge.Client {
	switch name {
	case "github":
		if t := os.Getenv("GH_TOKEN"); t != "" || os.Getenv("BRIDGE_GITHUB_API") != "" {
			return forge.NewGithubClient(t, os.Getenv("BRIDGE_GITHUB_API"))
		}
	case "gitlab":
		if t := os.Getenv("GITLAB_TOKEN"); t != "" || os.Getenv("BRIDGE_GITLAB_API") != "" {
			return forge.NewGitlabClient(t, os.Getenv("BRIDGE_GITLAB_API"))
		}
	case "forgejo":
		if t := os.Getenv("FORGEJO_TOKEN"); t != "" || os.Getenv("BRIDGE_FORGEJO_API") != "" {
			return forge.NewForgejoClient(t, os.Getenv("BRIDGE_FORGEJO_API"))
		}
	}
	return nil
}
