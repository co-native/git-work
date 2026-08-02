package tui

import "testing"

func TestErrNoTTYMessage(t *testing.T) {
	if ErrNoTTY == nil {
		t.Fatal("ErrNoTTY should be a sentinel error")
	}
	if got := ErrNoTTY.Error(); got == "" {
		t.Error("ErrNoTTY message empty")
	}
}
