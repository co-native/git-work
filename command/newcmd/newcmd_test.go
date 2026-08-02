package newcmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/state"
)

// Help is intercepted by Run before parseFlags, so the descriptor and the
// registered flags are what must agree; see TestRunHelp for the behaviour.
func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

// -h wins over the mandatory positional: help is answered before parsing, so
// a bare `git work new -h` prints usage and succeeds.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		var code int
		out, _ := captureOutput(t, func() { code = Run([]string{arg}) })
		if code != cli.OK {
			t.Fatalf("Run(%s) = %d; want %d", arg, code, cli.OK)
		}
		for _, want := range []string{"usage: git work new", "--no-ticket", "--repos", "--description", "--dir", "--branch", "--reuse-existing", "--always-new", "--non-interactive"} {
			if !strings.Contains(out, want) {
				t.Errorf("help output = %q; want %q", out, want)
			}
		}
	}
}

// A malformed invocation exits 2 and prints the usage block, distinct from
// work that failed (1).
func TestRunUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                      // no positional
		{"--repos", "a"},         // flags but no positional
		{"PROJ-1", "stray"},      // a second positional
		{"PROJ-1", "--nonesuch"}, // an unknown flag
	} {
		var code int
		_, errOut := captureOutput(t, func() { code = Run(args) })
		if code != cli.Usage {
			t.Errorf("Run(%v) = %d; want %d", args, code, cli.Usage)
		}
		if !strings.Contains(errOut, "usage: git work new") {
			t.Errorf("Run(%v) stderr = %q; want the usage block", args, errOut)
		}
	}
}

// captureOutput runs f with os.Stdout/os.Stderr redirected, returning both.
func captureOutput(t *testing.T, f func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	wOut.Close()
	wErr.Close()
	out, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(errOut)
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix login redirect":      "fix-login-redirect",
		"  Trailing & symbols!! ": "trailing-symbols",
		"CamelCase Words":         "camelcase-words",
		"already-a-slug":          "already-a-slug",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q want %q", in, got, want)
		}
	}
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"PROJ-7", "--repos", "a,b", "--branch", "feat-x", "--non-interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if o.ticketID != "PROJ-7" || o.branch != "feat-x" || !o.nonInteractive {
		t.Errorf("opts = %+v", o)
	}
	if len(o.repos) != 2 || o.repos[0] != "a" {
		t.Errorf("repos = %v", o.repos)
	}
	if o.noTicket {
		t.Error("noTicket should default to false")
	}
}

func TestParseFlagsNoTicket(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"flag before name", []string{"--no-ticket", "spike", "--repos", "a"}},
		{"flag after name", []string{"spike", "--no-ticket", "--repos", "a"}},
		{"shorthand before name", []string{"-n", "spike", "--repos", "a"}},
		{"shorthand after name", []string{"spike", "--repos", "a", "-n"}},
	}
	for _, tt := range cases {
		o, err := parseFlags(tt.args)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !o.noTicket || o.ticketID != "spike" {
			t.Errorf("%s: opts = %+v", tt.name, o)
		}
		if len(o.repos) != 1 || o.repos[0] != "a" {
			t.Errorf("%s: repos = %v", tt.name, o.repos)
		}
	}
}

// A stray second positional must be an error: the flag package stops
// parsing at the first non-flag token, so accepting it would silently
// discard the stray AND every flag after it.
func TestParseFlagsStrayPositional(t *testing.T) {
	cases := [][]string{
		{"PROJ-1", "stray"},
		{"PROJ-1", "stray", "--repos", "a,b", "--non-interactive"},
		{"-n", "my", "feature"},
		{"PROJ-1", "--repos", "a", "stray"},
	}
	for _, args := range cases {
		if _, err := parseFlags(args); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
			t.Errorf("parseFlags(%v) err = %v, want an unexpected-argument error", args, err)
		}
	}
}

func TestParseFlagsNoPositional(t *testing.T) {
	for _, args := range [][]string{nil, {"--no-ticket"}, {"--repos", "a"}} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%v) should error without a positional arg", args)
		}
	}
}

// TestNewNoTicket covers ticketless mode end to end: `new --no-ticket <name>`
// derives folder and branch from the plain name, does no provider work at all,
// writes no TICKET.md (also after refresh), and - the unchanged error path -
// without the flag a ticket id matching no configured provider is an error,
// never a silent ticketless folder.
func TestNewNoTicket(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "git-work")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "../../.").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	home := t.TempDir()
	reposRoot := filepath.Join(home, "dev", "repos")
	workRoot := filepath.Join(home, "dev", "work")

	// Config with a github provider so the non-match error path is armed.
	cfgDir := filepath.Join(home, ".config", "git-work")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "paths:\n  repos: " + reposRoot + "\n  work: " + workRoot + "\n" +
		"ticket_providers:\n" +
		"  - name: gh\n" +
		"    type: github\n" +
		"    patterns:\n" +
		"      - prefix: dev-tools\n" +
		"        repo: o/api\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a bare origin with one commit so we can clone it.
	origin := filepath.Join(home, "api.git")
	newcmdSh(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(home, "seed-api")
	newcmdSh(t, "", "git", "clone", "-q", origin, seed)
	newcmdSh(t, seed, "git", "config", "user.email", "t@t.t")
	newcmdSh(t, seed, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newcmdSh(t, seed, "git", "add", "f.txt")
	newcmdSh(t, seed, "git", "commit", "-qm", "init")
	newcmdSh(t, seed, "git", "push", "-q", "origin", "main")

	env := append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	gw := func(dir string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := gw("", "clone", origin, "api"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}

	// Ticketless new: folder and branch derived from the plain name.
	out, err := gw("", "new", "--no-ticket", "Spike Harness", "--repos", "api", "--non-interactive")
	if err != nil {
		t.Fatalf("new --no-ticket: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "spike-harness")
	if _, err := os.Stat(workDir); err != nil {
		t.Fatalf("work folder spike-harness not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "TICKET.md")); !os.IsNotExist(err) {
		t.Errorf("TICKET.md must not exist in a ticketless folder (stat err = %v)", err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !st.NoTicket || st.TicketID != "" || st.Provider != "" {
		t.Errorf("state = %+v, want NoTicket with empty ticket/provider", st)
	}
	if st.Title != "Spike Harness" || st.Slug != "spike-harness" || st.Branch != "spike-harness" {
		t.Errorf("state = %+v, want name-derived slug/branch", st)
	}
	readme, err := os.ReadFile(filepath.Join(workDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "# Spike Harness\n") {
		t.Errorf("README.md heading should be the name:\n%s", readme)
	}
	claude, err := os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), "# Spike Harness\nBranch: spike-harness\n") {
		t.Errorf("CLAUDE.md heading should be the name without a ticket prefix:\n%s", claude)
	}

	// The worktree must be on the name-derived branch.
	wt := filepath.Join(workDir, "api")
	head := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD")
	if b, err := head.CombinedOutput(); err != nil || strings.TrimSpace(string(b)) != "spike-harness" {
		t.Errorf("worktree branch = %q (err %v), want spike-harness", strings.TrimSpace(string(b)), err)
	}

	// refresh must not resurrect TICKET.md for a ticketless folder.
	if out, err := gw(workDir, "refresh", "--no-pull"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "TICKET.md")); !os.IsNotExist(err) {
		t.Errorf("refresh recreated TICKET.md in a ticketless folder (stat err = %v)", err)
	}

	// Unchanged error path: without --no-ticket a ticket id that matches no
	// configured provider errors instead of silently going ticketless.
	out, err = gw("", "new", "nope-1", "--repos", "api", "--non-interactive")
	if err == nil {
		t.Fatalf("new with non-matching ticket id should fail, got:\n%s", out)
	}
	if !strings.Contains(out, "no-ticket") {
		t.Errorf("error should point at --no-ticket, got:\n%s", out)
	}
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "spike-harness" {
		t.Errorf("non-matching ticket id must not create a work folder, work root has %v", entries)
	}
}

// newcmdSh runs a command in dir, failing the test on error.
func newcmdSh(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// TestNewSplitCasing verifies that folder_case and branch_case are applied
// independently: a github provider with branch_case: lower (and no folder_case
// override, defaulting to upper) should produce an uppercase work-folder name
// and a lowercase branch name.
func TestNewSplitCasing(t *testing.T) {
	// Build the binary.
	bin := filepath.Join(t.TempDir(), "git-work")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "../../.").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	home := t.TempDir()
	reposRoot := filepath.Join(home, "dev", "repos")
	workRoot := filepath.Join(home, "dev", "work")

	// Write config with a github provider whose branch_case is lower and
	// folder_case is unset (so it defaults to upper).
	cfgDir := filepath.Join(home, ".config", "git-work")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "paths:\n  repos: " + reposRoot + "\n  work: " + workRoot + "\n" +
		"ticket_providers:\n" +
		"  - name: gh\n" +
		"    type: github\n" +
		"    branch_case: lower\n" +
		"    patterns:\n" +
		"      - prefix: dev-tools\n" +
		"        repo: o/api\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a bare origin with one commit so we can clone it.
	origin := filepath.Join(home, "api.git")
	newcmdSh(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(home, "seed-api")
	newcmdSh(t, "", "git", "clone", "-q", origin, seed)
	newcmdSh(t, seed, "git", "config", "user.email", "t@t.t")
	newcmdSh(t, seed, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newcmdSh(t, seed, "git", "add", "f.txt")
	newcmdSh(t, seed, "git", "commit", "-qm", "init")
	newcmdSh(t, seed, "git", "branch", "-M", "main")
	newcmdSh(t, seed, "git", "push", "-q", "origin", "main")

	env := append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))

	gw := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Clone into repos root.
	if out, err := gw("clone", origin, "api"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}

	// Run new non-interactively; the github Fetch will fail (no gh CLI in
	// tests) but newcmd continues without ticket details, using displayID as
	// the title (DEV-TOOLS-1).
	out, err := gw("new", "dev-tools-1", "--repos", "api", "--non-interactive")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}

	// Work folder must be uppercase: DEV-TOOLS-1-<slug>
	entries, readErr := os.ReadDir(workRoot)
	if readErr != nil {
		t.Fatalf("read work root: %v", readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one work folder, got %v", entries)
	}
	folderName := entries[0].Name()
	if !strings.HasPrefix(folderName, "DEV-TOOLS-1-") {
		t.Errorf("work folder name = %q, want prefix DEV-TOOLS-1- (folder_case defaults to upper)", folderName)
	}

	// Branch in state must be lowercase: dev-tools-1-<slug>
	workDir := filepath.Join(workRoot, folderName)
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !strings.HasPrefix(st.Branch, "dev-tools-1-") {
		t.Errorf("state.Branch = %q, want prefix dev-tools-1- (branch_case: lower)", st.Branch)
	}
}

func TestDefaultRepos(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"api":     {AddByDefault: true},
		"web":     {AddByDefault: true},
		"docs":    {AddByDefault: false},
		"ghost":   {AddByDefault: true}, // configured but not cloned here
		"tooling": {Integration: config.IntegrationPR},
	}}
	// ListRepos order is what the picker renders, so the ticks must follow it.
	got := defaultRepos(cfg, []string{"web", "docs", "api", "tooling"})
	want := []string{"web", "api"}
	if len(got) != len(want) {
		t.Fatalf("defaultRepos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("defaultRepos = %v, want %v (order follows available)", got, want)
		}
	}
}

func TestDefaultReposEmpty(t *testing.T) {
	if got := defaultRepos(&config.Config{}, []string{"api"}); len(got) != 0 {
		t.Fatalf("defaultRepos with no config = %v, want empty", got)
	}
}
