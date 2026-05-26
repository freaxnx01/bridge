package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// pickRepo runs the picker against the supplied repos and returns the chosen
// one. Returns (Repo{}, false, nil) if the user cancelled (Esc/Ctrl-C).
//
// Test hooks:
//
//	BRIDGE_PICKER_FIXTURE — return the repo with the named Name.
//	BRIDGE_PICKER_FIXTURE_CANCEL — return (Repo{}, false, nil).
func pickRepo(repos []core.Repo) (core.Repo, bool, error) {
	if os.Getenv("BRIDGE_PICKER_FIXTURE_CANCEL") != "" {
		return core.Repo{}, false, nil
	}
	if name := os.Getenv("BRIDGE_PICKER_FIXTURE"); name != "" {
		r, ok := findRepoByName(repos, name)
		return r, ok, nil
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		return core.Repo{}, false, errors.New("fzf not found in PATH; install fzf or set BRIDGE_DEFAULT_AGENT to skip picker")
	}
	sort.Slice(repos, func(i, j int) bool { return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name) })
	var input bytes.Buffer
	for _, r := range repos {
		input.WriteString(r.Name + "\t" + r.Path + "\n")
	}
	cmd := exec.Command("fzf", "--with-nth=1", "--delimiter=\t", "--prompt=bridge> ")
	cmd.Stdin = &input
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return core.Repo{}, false, nil
		}
		return core.Repo{}, false, err
	}
	chosen := strings.TrimSpace(out.String())
	if chosen == "" {
		return core.Repo{}, false, nil
	}
	parts := strings.SplitN(chosen, "\t", 2)
	if len(parts) != 2 {
		return core.Repo{}, false, errors.New("picker: malformed selection")
	}
	for _, r := range repos {
		if r.Path == parts[1] {
			return r, true, nil
		}
	}
	return core.Repo{}, false, errors.New("picker: chosen repo not in list")
}
