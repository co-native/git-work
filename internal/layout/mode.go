package layout

import "fmt"

// Mode selects how a work folder holds its repos: as linked worktrees of the
// primary clones, or as full clones of their own.
type Mode string

const (
	// ModeWorktree is the original layout: one linked worktree per repo under
	// the work folder, backed by a primary clone under the repos root.
	ModeWorktree Mode = "worktree"
	// ModeClone is primary-free: one full clone per repo under the work folder.
	ModeClone Mode = "clone"
)

// Modes lists the accepted mode values, for validation and help text.
var Modes = []Mode{ModeWorktree, ModeClone}

// ParseMode validates a mode value. The empty string means worktree, so a
// config or state file written before modes existed keeps working.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", ModeWorktree:
		return ModeWorktree, nil
	case ModeClone:
		return ModeClone, nil
	default:
		return "", fmt.Errorf("invalid mode %q (want %s or %s)", s, ModeWorktree, ModeClone)
	}
}

// OrDefault resolves the zero value to worktree.
func (m Mode) OrDefault() Mode {
	if m == "" {
		return ModeWorktree
	}
	return m
}
