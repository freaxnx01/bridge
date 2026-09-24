package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestPortInUseMessage(t *testing.T) {
	tests := []struct {
		name      string
		accepting bool
		freePort  int
		want      []string
		notWant   []string
	}{
		{
			name:      "visible holder, free neighbour found",
			accepting: true,
			freePort:  7789,
			want:      []string{"127.0.0.1:7788 is already in use", "--port 7789", "client"},
			notWant:   []string{"doesn't show"},
		},
		{
			name:      "hidden holder mentions invisible reservation",
			accepting: false,
			freePort:  7789,
			want:      []string{"already in use", "--port 7789", "doesn't show", "WSL"},
		},
		{
			name:      "no free neighbour falls back to a generic hint",
			accepting: true,
			freePort:  0,
			want:      []string{"--port <n>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portInUseMessage("127.0.0.1", 7788, tt.accepting, tt.freePort)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("message %q lacks %q", got, w)
				}
			}
			for _, nw := range tt.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("message %q must not contain %q", got, nw)
				}
			}
		})
	}
}

func TestListenMCP_FreePort_Listens(t *testing.T) {
	ln, err := listenMCP("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("listen on a free port: %v", err)
	}
	defer ln.Close() //nolint:errcheck // test cleanup
}

func TestListenMCP_PortTakenByListener_SuggestsPort(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close() //nolint:errcheck // test cleanup
	port := holder.Addr().(*net.TCPAddr).Port

	_, err = listenMCP("127.0.0.1", port)
	if err == nil {
		t.Fatal("want an error when the port is taken")
	}
	msg := err.Error()
	for _, w := range []string{fmt.Sprintf("127.0.0.1:%d is already in use", port), "--port"} {
		if !strings.Contains(msg, w) {
			t.Errorf("error %q lacks %q", msg, w)
		}
	}
	if strings.Contains(msg, "doesn't show") {
		t.Errorf("a visible listener must not trigger the hidden-holder hint: %q", msg)
	}
}

func TestIsAddrInUse(t *testing.T) {
	if isAddrInUse(errors.New("boom")) {
		t.Error("an unrelated error must not count as address-in-use")
	}
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close() //nolint:errcheck // test cleanup
	_, err = net.Listen("tcp", holder.Addr().String())
	if !isAddrInUse(err) {
		t.Errorf("a real bind conflict must count as address-in-use: %v", err)
	}
}
