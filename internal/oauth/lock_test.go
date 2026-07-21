package oauth

import "testing"

func TestOpenStore_SecondInstance_IsRejected(t *testing.T) {
	dir := t.TempDir()

	first, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("first OpenStore: %v", err)
	}
	defer first.Close()

	second, err := OpenStore(dir)
	if err == nil {
		second.Close()
		t.Fatal("second OpenStore succeeded; concurrent instances would clobber state")
	}

	// Releasing the first must let a new instance in.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	third, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore after release: %v", err)
	}
	third.Close()
}
