//go:build windows

package main

import (
	"os"
	"os/exec"
)

// osExecSelf returns an *exec.Cmd that re-launches the bridge-go binary with
// argv. On Windows there is no setsid equivalent; the process is simply
// started detached with no stdio.
func osExecSelf(argv []string) *exec.Cmd {
	self, _ := os.Executable()
	c := exec.Command(self, argv...)
	c.Stdin = nil
	c.Stdout = nil
	c.Stderr = nil
	return c
}
