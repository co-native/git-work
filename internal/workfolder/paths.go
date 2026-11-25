package workfolder

import (
	"os"
	"path/filepath"

	"github.com/co-native/git-work/internal/layout"
)

// ResolveDir resolves a work folder argument the way `done` and `rename` read
// it: a bare name (no path separator) resolves under the work root, anything
// containing a separator or an absolute path is treated as a path. The result
// is absolute.
func ResolveDir(l layout.Layout, arg string) (string, error) {
	dir := arg
	if !containsPathSeparator(dir) && !filepath.IsAbs(dir) {
		dir = l.WorkDir(dir)
	}
	return filepath.Abs(dir)
}

// InsideDir reports whether child is parent or a descendant of it. A failed
// Stat, an absolute Rel (a different volume on Windows), ".." and "../x" all
// mean outside; everything else is inside, including "." and names that
// merely begin with a dot.
func InsideDir(parent, child string) bool {
	// Walk up from child and compare by file identity. That handles symlinks
	// (macOS /var vs /private/var) and case-insensitive paths, neither of which
	// a string-prefix or filepath.Rel check gets right.
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return false
	}
	for {
		info, err := os.Stat(child)
		if err != nil {
			return false
		}
		if os.SameFile(parentInfo, info) {
			return true
		}
		next := filepath.Dir(child)
		if next == child {
			return false
		}
		child = next
	}
}

// containsPathSeparator reports whether s holds a path separator for this OS.
//
// Not `strings.ContainsRune(s, filepath.Separator)`: on Windows that only
// matches `\`, so `git work done proj/PROJ-1` - the spelling a Windows
// operator typing a path is most likely to use - would be mistaken for a bare
// folder name and resolved under the work root. os.IsPathSeparator accepts
// both there while still treating a backslash as an ordinary filename
// character on Unix.
func containsPathSeparator(s string) bool {
	for i := 0; i < len(s); i++ {
		if os.IsPathSeparator(s[i]) {
			return true
		}
	}
	return false
}
