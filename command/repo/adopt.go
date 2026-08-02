package repo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// AdoptCmd describes `git work repo adopt`.
var AdoptCmd = &cli.Command{
	Name:     "repo adopt",
	Short:    "adopt an existing clone into the repos root; re-run to fix drift",
	Args:     "<dir>",
	Synopsis: []string{"git work repo adopt <dir>"},
	Long: "Moves an existing plain clone into the managed layout - checks out the default\n" +
		"branch, moves the clone to <repos-root>/<repo>/<default-branch>, writes the\n" +
		"parent .git pointer file, sets core.worktree, and repairs linked worktrees'\n" +
		"back-pointers. Re-run against an already-managed repo to normalize layout\n" +
		"drift instead; a conforming repo is a no-op. <dir> is a path, or a bare name\n" +
		"looked up under the repos root and its parent (a name in both needs a path).",
	Examples: []string{
		"git work repo adopt ~/dev/api",
		"git work repo adopt api",
	},
}

// runAdopt executes `git work repo adopt <dir>`.
func runAdopt(args []string) error {
	if len(args) != 1 {
		return cli.Usagef("give exactly one <dir>")
	}
	// Help never reaches here (Run intercepts it), so a leading dash is a
	// stray flag rather than a help request; adopt has no flags of its own.
	if strings.HasPrefix(args[0], "-") {
		return cli.Usagef("unknown flag %q; adopt takes a single <dir>", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	src, err := resolveSource(args[0], l.ReposRoot)
	if err != nil {
		return err
	}
	return adopt(os.Stdout, l, src)
}

// resolveSource turns the <dir> argument into an absolute existing directory.
// An argument containing a path separator (or starting with "." or "~/") is
// treated as a path. A bare name is looked up under the repos root (the
// re-run/normalize case) and under the repos root's parent - where plain
// clones live, e.g. ~/dev/<dir> with the default config; a name present in
// both places is ambiguous and needs a path.
//
// Its failures are usage errors rather than operation failures: each one says
// the <dir> argument names the wrong thing, and the remedy is to retype the
// command with a different (or more specific) argument.
func resolveSource(arg, reposRoot string) (string, error) {
	if arg == "~" || strings.HasPrefix(arg, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
	}
	if strings.ContainsRune(arg, os.PathSeparator) || strings.HasPrefix(arg, ".") {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return "", err
		}
		if !isDir(abs) {
			return "", cli.Usagef("%s: not an existing directory", abs)
		}
		return abs, nil
	}
	managed := filepath.Join(reposRoot, arg)
	dev := filepath.Join(filepath.Dir(reposRoot), arg)
	switch {
	case isDir(managed) && isDir(dev):
		return "", cli.Usagef("%q is ambiguous: both %s and %s exist; pass a path", arg, managed, dev)
	case isDir(managed):
		return managed, nil
	case isDir(dev):
		return dev, nil
	}
	return "", cli.Usagef("%q not found (tried %s and %s); pass a path", arg, dev, managed)
}

// isDir reports whether path is an existing directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// adopt routes src to fresh adoption or drift normalization: a directory
// already under the repos root is an existing managed repo to normalize;
// anything else is a plain clone to move into the managed layout.
func adopt(w io.Writer, l layout.Layout, src string) error {
	src = filepath.Clean(src)
	root := filepath.Clean(l.ReposRoot)
	sep := string(os.PathSeparator)
	if rel, err := filepath.Rel(root, src); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+sep) {
		if rel == "." {
			return fmt.Errorf("%s is the repos root itself, not a repo", src)
		}
		parts := strings.Split(rel, sep)
		if len(parts) > 2 {
			return fmt.Errorf("%s is nested too deep under the repos root; pass <repos-root>/<repo> or <repos-root>/<repo>/<primary>", src)
		}
		return normalize(w, l, parts[0])
	}
	return adoptFresh(w, l, src)
}

// adoptFresh moves the plain clone at src into the managed layout:
// <repos-root>/<name>/<default-branch> with the default branch checked out,
// the parent .git pointer file, core.worktree "..", and repaired worktree
// back-pointers.
func adoptFresh(w io.Writer, l layout.Layout, src string) error {
	if err := checkPlainClone(src); err != nil {
		return err
	}
	name := filepath.Base(src)
	repoDir := l.RepoDir(name)
	if _, err := os.Stat(repoDir); err == nil {
		return fmt.Errorf("%s already exists; adopt it by name to normalize it, or remove it first", repoDir)
	}
	def, err := gitrepo.DefaultBranchName(src)
	if err != nil {
		return err
	}
	// A primary must sit on its default branch: day-to-day pulls and
	// rebases target it, and the folder is about to be named after it.
	// Checking out before anything moves means a failure (dirty tree)
	// aborts cleanly.
	if cur, cerr := gitrepo.CurrentBranch(src); cerr != nil || cur != def {
		if err := gitrepo.Checkout(src, def); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: checked out the default branch %s\n", name, def)
	}
	linked, err := linkedWorktrees(src)
	if err != nil {
		return err
	}
	primary := filepath.Join(repoDir, def)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, primary); err != nil {
		return err
	}
	if err := writePointer(repoDir, def); err != nil {
		return err
	}
	if err := gitrepo.SetCoreWorktree(primary, ".."); err != nil {
		return err
	}
	fmt.Fprintf(w, "Adopted %s -> %s\n", src, primary)
	return repairLinked(w, primary, linked)
}

// checkPlainClone verifies src is a real clone (a .git directory). A .git
// pointer file means src is either an already-managed parent dir or a linked
// worktree - neither is a clone that can be moved.
func checkPlainClone(src string) error {
	fi, err := os.Stat(filepath.Join(src, ".git"))
	if err != nil {
		return fmt.Errorf("%s is not a git clone (no .git)", src)
	}
	if fi.IsDir() {
		return nil
	}
	if _, err := layout.PrimaryDir(src); err == nil {
		return fmt.Errorf("%s already has the managed primary-clone layout; move it under the repos root by hand, then adopt it to normalize", src)
	}
	return fmt.Errorf("%s is a linked worktree, not a clone; adopt its primary clone instead", src)
}

// normalize re-runs adoption against the managed repo name: renames the
// primary folder to match the default branch, rewrites the parent .git
// pointer file, sets core.worktree, checks out the default branch, and
// repairs worktree back-pointers. A conforming repo is a no-op with a clear
// message.
func normalize(w io.Writer, l layout.Layout, name string) error {
	repoDir := l.RepoDir(name)
	primary, perr := layout.PrimaryDir(repoDir)
	pointerOK := perr == nil
	if !pointerOK {
		if primary = primaryFallback(repoDir); primary == "" {
			return fmt.Errorf("cannot locate the primary clone in %s: %v", repoDir, perr)
		}
	}
	mainDir := filepath.Join(repoDir, primary)
	def, err := gitrepo.DefaultBranchName(mainDir)
	if err != nil {
		return err
	}
	cw, err := gitrepo.CoreWorktree(mainDir)
	if err != nil {
		return err
	}
	cur, curErr := gitrepo.CurrentBranch(mainDir)

	needRename := primary != def
	needPointer := !pointerOK || needRename
	needCoreWorktree := cw != ".."
	// A detached HEAD (curErr != nil) also gets the default branch back.
	needCheckout := curErr != nil || cur != def
	if !needRename && !needPointer && !needCoreWorktree && !needCheckout {
		fmt.Fprintf(w, "%s already conforms to the managed layout (primary %s); nothing to do\n", name, def)
		return nil
	}

	var linked []gitrepo.Worktree
	if needRename {
		if linked, err = linkedWorktrees(mainDir); err != nil {
			return err
		}
		newMain := filepath.Join(repoDir, def)
		if _, err := os.Stat(newMain); err == nil {
			return fmt.Errorf("cannot rename primary %s -> %s: %s already exists", primary, def, newMain)
		}
		if err := os.Rename(mainDir, newMain); err != nil {
			return err
		}
		mainDir = newMain
		fmt.Fprintf(w, "%s: renamed primary %s -> %s\n", name, primary, def)
	}
	if needPointer {
		if err := writePointer(repoDir, def); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: wrote .git pointer file (gitdir: ./%s/.git)\n", name, def)
	}
	if needCoreWorktree {
		if err := gitrepo.SetCoreWorktree(mainDir, ".."); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: set core.worktree ..\n", name)
	}
	if needCheckout {
		if err := gitrepo.Checkout(mainDir, def); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: checked out the default branch %s\n", name, def)
	}
	return repairLinked(w, mainDir, linked)
}

// writePointer writes the parent .git pointer file naming the primary dir.
func writePointer(repoDir, primary string) error {
	return os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./"+primary+"/.git\n"), 0o644)
}

// linkedWorktrees returns the repo's linked worktrees (the primary itself
// excluded).
func linkedWorktrees(mainDir string) ([]gitrepo.Worktree, error) {
	wts, err := gitrepo.ListWorktrees(mainDir)
	if err != nil {
		return nil, err
	}
	if len(wts) <= 1 {
		return nil, nil
	}
	return wts[1:], nil
}

// repairLinked rewrites the .git back-pointers of linked worktrees after the
// primary clone moved (git worktree repair).
func repairLinked(w io.Writer, mainDir string, linked []gitrepo.Worktree) error {
	if len(linked) == 0 {
		return nil
	}
	paths := make([]string, len(linked))
	for i, wt := range linked {
		paths[i] = wt.Path
	}
	if err := gitrepo.RepairWorktrees(mainDir, paths...); err != nil {
		return err
	}
	fmt.Fprintf(w, "Repaired %d worktree back-pointer(s)\n", len(linked))
	return nil
}
