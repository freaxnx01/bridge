//go:build !windows

package main

import (
	"strings"
	"syscall"
	"testing"
)

// A socket that is bound but never listens holds the port without accepting
// connections — the closest portable stand-in for a reservation netstat
// doesn't show.
func TestListenMCP_PortHeldWithoutListener_MentionsHiddenHolder(t *testing.T) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) }) // test teardown; nothing to recover
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	port := sa.(*syscall.SockaddrInet4).Port

	_, err = listenMCP("127.0.0.1", port)
	if err == nil {
		t.Fatal("want an error when the port is held")
	}
	if !strings.Contains(err.Error(), "doesn't show") {
		t.Errorf("error %q lacks the hidden-holder hint", err)
	}
}
