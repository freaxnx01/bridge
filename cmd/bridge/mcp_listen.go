package main

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// freePortProbeRange is how many ports above the requested one are tried when
// suggesting an alternative --port.
const freePortProbeRange = 10

// listenMCP binds the MCP listener. A port conflict comes back as an error
// that says so and suggests a --port that is free right now, so a dead
// server can be restarted without guesswork.
func listenMCP(host string, port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", joinHostPort(host, port))
	if err == nil {
		return ln, nil
	}
	if !isAddrInUse(err) {
		return nil, err
	}
	msg := portInUseMessage(host, port, isAccepting(host, port), findFreePort(host, port+1, freePortProbeRange))
	return nil, fmt.Errorf("%s: %w", msg, err)
}

// portInUseMessage explains a bind conflict. accepting reports whether
// anything answers on the port; when nothing does, the holder is likely
// invisible to netstat. freePort is a port that bound successfully, or 0.
func portInUseMessage(host string, port int, accepting bool, freePort int) string {
	msg := joinHostPort(host, port) + " is already in use"
	if !accepting {
		msg += " — yet nothing accepts connections on it, so it may be held by something the OS doesn't show" +
			" (on Windows with WSL2 mirrored networking, possibly a stale reservation that `wsl --shutdown` may release)"
	}
	suggestion := "--port <n>"
	if freePort != 0 {
		suggestion = "--port " + strconv.Itoa(freePort)
	}
	return msg + "; restart with " + suggestion + " and point the MCP client at the same port"
}

// isAccepting reports whether a TCP connection to host:port succeeds.
func isAccepting(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", joinHostPort(probeHost(host), port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close() // the probe carries no data; a failed close changes nothing
	return true
}

// findFreePort returns the first port in [from, from+count) that binds on
// host, or 0 when none does.
func findFreePort(host string, from, count int) int {
	for port := from; port < from+count && port <= 65535; port++ {
		ln, err := net.Listen("tcp", joinHostPort(host, port))
		if err != nil {
			continue
		}
		_ = ln.Close() // only a suggestion; the real bind reports its own error
		return port
	}
	return 0
}

// probeHost maps a wildcard bind host to the loopback address of the same
// family: dialing 0.0.0.0 or :: fails on Windows, which would misreport a
// visible listener as a hidden one.
func probeHost(host string) string {
	ip := net.ParseIP(host)
	switch {
	case host == "" || (ip != nil && ip.Equal(net.IPv4zero)):
		return "127.0.0.1"
	case ip != nil && ip.IsUnspecified():
		return "::1"
	}
	return host
}

func joinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
