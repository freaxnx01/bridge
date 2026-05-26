//go:build windows

package launcher

import "testing"

func TestWTNew(t *testing.T) {
	l := New()
	if l == nil {
		t.Fatal("nil")
	}
}
