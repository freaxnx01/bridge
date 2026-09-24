//go:build windows

package main

import (
	"errors"
	"syscall"
)

// wsaeAddrInUse is WSAEADDRINUSE ("Only one usage of each socket address…").
// Go's syscall.EADDRINUSE is an invented value on Windows and never matches it.
const wsaeAddrInUse = syscall.Errno(10048)

func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeAddrInUse)
}
