package version

import (
	"runtime"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli/clitest"
)

func TestCmdConformsToHelpContract(t *testing.T) {
	clitest.Check(t, Cmd, nil)
}

func TestStringIdentifiesTheBuild(t *testing.T) {
	// The line's job is to be pasted into a bug report, so it has to carry
	// enough to identify a build even when VCS stamping is absent (as it is
	// under `go test`, which builds from a temp module).
	s := String()
	if !strings.HasPrefix(s, "git-work ") {
		t.Errorf("String() = %q; want it to lead with the binary name", s)
	}
	for _, want := range []string{runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q; want it to contain %q", s, want)
		}
	}
	if strings.Contains(s, "\n") {
		t.Errorf("String() = %q; want a single line", s)
	}
}

func TestInfoDegradesWithoutBuildInfo(t *testing.T) {
	// A version command that errors is worse than one admitting it cannot
	// tell, so an unset version must still render something usable.
	b := build{version: "(unknown)"}
	if b.dirtyMark() != "" {
		t.Errorf("dirtyMark() = %q for a clean build; want empty", b.dirtyMark())
	}
	b.dirty = true
	if b.dirtyMark() != " dirty" {
		t.Errorf("dirtyMark() = %q for a dirty build; want %q", b.dirtyMark(), " dirty")
	}
}
