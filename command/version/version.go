// Package version implements `git work version`: the build's identity, in one
// line meant to be pasted into a bug report.
//
// Everything comes from runtime/debug.ReadBuildInfo rather than -ldflags. The
// binary is distributed with `go install`, which never runs the Makefile, so an
// ldflags-stamped variable would read "unknown" for exactly the people most
// likely to be reporting a problem. Build info is populated either way: the
// module version for `go install module@version`, and the VCS revision plus
// dirty flag for a build from a checkout.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/co-native/git-work/internal/cli"
)

// Cmd describes `git work version` for the help system. main.go builds its
// index row from Short and answers `git work help version` from the same value
// that `git work version -h` prints. version parses no flags of its own, so the
// descriptor declares none - the renderer still appends -h/--help.
var Cmd = &cli.Command{
	Name:     "version",
	Short:    "print the git-work version",
	Synopsis: []string{"git work version"},
	Long: "Prints the build's version, VCS revision and build date, the Go version it\n" +
		"was built with, and the target platform - the line to include in a bug\n" +
		"report. `git work --version` prints the same thing.\n\n" +
		"A release install reports its tag (v1.2.3). A build from a checkout reports\n" +
		"(devel) plus the commit it came from, marked dirty when the tree had\n" +
		"uncommitted changes at build time.",
}

// Run executes `git work version`; it returns the process exit code.
func Run(args []string) int {
	if cli.Wanted(args) {
		return cli.Help(Cmd)
	}
	fmt.Println(String())
	return cli.OK
}

// String renders the one-line version banner.
func String() string {
	b := info()
	s := "git-work " + b.version
	switch {
	case b.revision != "" && b.date != "":
		s += fmt.Sprintf(" (%s%s, %s)", b.revision, b.dirtyMark(), b.date)
	case b.revision != "":
		s += fmt.Sprintf(" (%s%s)", b.revision, b.dirtyMark())
	}
	return s + " " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}

// build holds the parts of debug.BuildInfo this command reports.
type build struct {
	version  string
	revision string
	date     string
	dirty    bool
}

func (b build) dirtyMark() string {
	if b.dirty {
		return " dirty"
	}
	return ""
}

// info reads the build's identity. Missing build info (a binary produced by
// something other than the go tool) degrades to "(unknown)" rather than
// failing: a version command that errors is worse than one that admits it
// cannot tell.
func info() build {
	b := build{version: "(unknown)"}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	if bi.Main.Version != "" {
		b.version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.revision = s.Value
			if len(b.revision) > 7 {
				b.revision = b.revision[:7]
			}
		case "vcs.time":
			// Date alone: the clock time adds noise to a line whose job is to
			// identify a build, and the revision already pins it exactly.
			b.date = s.Value
			if len(b.date) >= 10 {
				b.date = b.date[:10]
			}
		case "vcs.modified":
			b.dirty = s.Value == "true"
		}
	}
	return b
}
