package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var rmYes bool

var rmCmd = &cobra.Command{
	Use:               "rm <name>",
	Short:             "Delete a local repo (refuses without --yes if not a TTY)",
	Args:              cobra.ExactArgs(1),
	RunE:              runRm,
	ValidArgsFunction: completeRepoName,
}

func init() {
	rmCmd.Flags().BoolVar(&rmYes, "yes", false, "skip confirmation prompt")
	rootCmd.AddCommand(rmCmd)
}

func runRm(cmd *cobra.Command, args []string) error {
	name := args[0]
	repos, err := discoverAllRoots()
	if err != nil {
		return err
	}
	repo, ok := findRepoByName(repos, name)
	if !ok {
		fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown repo %q\n", name)
		os.Exit(2)
	}
	if !rmYes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintf(cmd.ErrOrStderr(), "bridge: refusing to delete without --yes (not a TTY)\n")
			os.Exit(2)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "delete %s? [y/N] ", repo.Path)
		var s string
		fmt.Fscanln(os.Stdin, &s)
		if s != "y" && s != "Y" && s != "yes" {
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}
	if err := os.RemoveAll(repo.Path); err != nil {
		return fmt.Errorf("rm: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", repo.Path)
	return nil
}
