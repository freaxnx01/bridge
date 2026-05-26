//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// osExecSelf returns an *exec.Cmd that re-launches the bridge-go binary with
// argv, detached from the current process group via setsid.
func osExecSelf(argv []string) *exec.Cmd {
	self, _ := os.Executable()
	c := exec.Command(self, argv...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Stdin = nil
	c.Stdout = nil
	c.Stderr = nil
	return c
}
