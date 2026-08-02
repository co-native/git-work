package tui

import (
	"errors"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

// ErrNoTTY is returned by interactive prompts when stdin is not a terminal.
var ErrNoTTY = errors.New("no interactive terminal; pass the corresponding flags instead")

// IsInteractive reports whether stdin is a TTY.
func IsInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd())
}

// MultiSelect asks the user to choose from options; returns selected values.
// MultiSelect prompts for any number of options. Entries listed in preselected
// start ticked; the operator still confirms, so a pre-selection is a shortcut
// rather than a decision made for them. Names in preselected that are not in
// options are ignored.
func MultiSelect(title string, options, preselected []string) ([]string, error) {
	if !IsInteractive() {
		return nil, ErrNoTTY
	}
	pre := make(map[string]bool, len(preselected))
	for _, p := range preselected {
		pre[p] = true
	}
	var selected []string
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o).Selected(pre[o])
	}
	err := huh.NewMultiSelect[string]().Title(title).Options(opts...).Value(&selected).Run()
	return selected, err
}

// Input prompts for a single line with a default value.
func Input(title, def string) (string, error) {
	if !IsInteractive() {
		return "", ErrNoTTY
	}
	val := def
	err := huh.NewInput().Title(title).Value(&val).Run()
	return val, err
}

// Select asks the user to pick one option.
func Select(title string, options []string) (string, error) {
	if !IsInteractive() {
		return "", ErrNoTTY
	}
	var selected string
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o)
	}
	err := huh.NewSelect[string]().Title(title).Options(opts...).Value(&selected).Run()
	return selected, err
}

// Confirm asks a yes/no question.
func Confirm(title string, def bool) (bool, error) {
	if !IsInteractive() {
		return false, ErrNoTTY
	}
	val := def
	err := huh.NewConfirm().Title(title).Value(&val).Run()
	return val, err
}
