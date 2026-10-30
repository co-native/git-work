package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestSectionsSeparatesBlocksThatPrint(t *testing.T) {
	var out strings.Builder
	s := NewSections(&out)

	s.Begin()
	fmt.Fprintln(s, "alpha: one")
	fmt.Fprintln(s, "  commit one")

	s.Begin()
	fmt.Fprintln(s, "beta: two")

	want := "alpha: one\n  commit one\n\nbeta: two\n"
	if got := out.String(); got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
}

func TestSectionsSkipsBlankLineForSilentBlock(t *testing.T) {
	var out strings.Builder
	s := NewSections(&out)

	s.Begin()
	fmt.Fprintln(s, "alpha: one")

	// A repo that only warns on stderr writes nothing here and must not leave
	// a blank line before the next block.
	s.Begin()

	s.Begin()
	fmt.Fprintln(s, "gamma: three")

	want := "alpha: one\n\ngamma: three\n"
	if got := out.String(); got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
}

func TestSectionsFirstBlockHasNoLeadingBlankLine(t *testing.T) {
	var out strings.Builder
	s := NewSections(&out)
	s.Begin()
	fmt.Fprint(s, "alpha")
	if got := out.String(); got != "alpha" {
		t.Errorf("output = %q; want %q", got, "alpha")
	}
}

func TestSectionsWritesOnlyThroughBeginBlocks(t *testing.T) {
	var out strings.Builder
	s := NewSections(&out)
	// No Begin: a bare write behaves like a plain writer and prints once.
	fmt.Fprint(s, "alpha")
	fmt.Fprint(s, "beta")
	if got := out.String(); got != "alphabeta" {
		t.Errorf("output = %q; want %q", got, "alphabeta")
	}
}
