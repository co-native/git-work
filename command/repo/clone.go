package repo

import (
	"fmt"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// CloneCmd describes `git work repo clone`, also reachable as the top-level
// `git work clone` alias.
var CloneCmd = &cli.Command{
	Name:  "repo clone",
	Short: "clone a repo into the primary repos root",
	Args:  "[opts] [--] <repo> [<dir>]",
	Synopsis: []string{
		"git work repo clone [opts] [--] <repo> [<dir>]",
		"git work clone [opts] [--] <repo> [<dir>]",
	},
	Long: "Clones <repo> into the repos root as its primary clone; the primary folder is\n" +
		"named after the upstream default branch, detected from origin/HEAD. <dir>\n" +
		"overrides the repo directory name, otherwise derived from the URL, and a <dir>\n" +
		"that itself looks like a repo URL is rejected - clone takes exactly one repo.\n" +
		"git-clone options must come before a `--` separator; without one, an arg\n" +
		"starting with `-` is ambiguous and rejected.",
	Examples: []string{
		"git work clone git@github.com:org/api.git",
		"git work clone --depth 1 -- git@github.com:org/api.git api",
	},
}

// parseCloneArgs splits args into git-clone options (everything before a "--"
// separator), the repo url, and an optional target dir. When no "--" is given,
// option flags are not allowed (they'd be ambiguous with flag values), so any
// arg starting with "-" is rejected with guidance to use "--". Help never
// reaches here: Run intercepts -h/--help before the subcommand's run function,
// and cli.Wanted stops scanning at the "--" that opens git-clone's own option
// space, so `clone -- -h` still reaches git verbatim.
// A <dir> that itself looks like a repo URL is rejected: clone takes exactly
// one repo, and a second URL is almost certainly a mistaken attempt to clone
// several at once (which would silently create a URL-named directory).
func parseCloneArgs(args []string) (opts []string, repoURL, dir string, err error) {
	for i, a := range args {
		if a == "--" {
			opts = args[:i]
			rest := args[i+1:]
			if len(rest) == 0 {
				return nil, "", "", cli.Usagef("missing <repo> after --")
			}
			repoURL = rest[0]
			if len(rest) > 1 {
				dir = rest[1]
			}
			return checkDir(opts, repoURL, dir)
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return nil, "", "", cli.Usagef("pass git clone options before a -- separator")
		}
	}
	if len(args) == 0 {
		return nil, "", "", cli.Usagef("give a <repo> to clone")
	}
	repoURL = args[0]
	if len(args) > 1 {
		dir = args[1]
	}
	return checkDir(opts, repoURL, dir)
}

// checkDir rejects a <dir> argument that looks like a repo URL.
func checkDir(opts []string, repoURL, dir string) ([]string, string, string, error) {
	if looksLikeRepoURL(dir) {
		return nil, "", "", cli.Usagef("%q looks like a repo URL, but clone takes a single <repo>; to clone several repos, run it once per URL", dir)
	}
	return opts, repoURL, dir, nil
}

// looksLikeRepoURL reports whether s resembles a clone URL rather than a
// directory name: a URL scheme, an scp-like user@host:path, or a .git suffix.
func looksLikeRepoURL(s string) bool {
	if strings.Contains(s, "://") || strings.HasSuffix(s, ".git") {
		return true
	}
	if at := strings.Index(s, "@"); at >= 0 && strings.Contains(s[at:], ":") {
		return true
	}
	return false
}

// deriveDir extracts a repo directory name from a clone URL/path.
func deriveDir(url string) string {
	u := strings.TrimSuffix(url, ".git")
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// runClone executes `git work repo clone` (also reachable through the
// top-level `git work clone` alias).
func runClone(args []string) error {
	opts, repoURL, dir, err := parseCloneArgs(args)
	if err != nil {
		return err
	}
	if dir == "" {
		dir = deriveDir(repoURL)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	target := l.RepoDir(dir)
	if err := gitrepo.Clone(repoURL, target, opts); err != nil {
		return err
	}
	fmt.Printf("Cloned %s -> %s\n", repoURL, target)
	return nil
}
