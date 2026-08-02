package done

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/state"
)

// Help is intercepted by Run before parseFlags, so the descriptor and the
// registered flags are what must agree; see TestRunHelp for the behaviour.
func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"PROJ-1-foo", "--delete-local", "--delete-remote", "--force", "--non-interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if o.dir != "PROJ-1-foo" || !o.deleteLocal || !o.deleteRemote || !o.force || !o.nonInteractive {
		t.Errorf("opts = %+v", o)
	}
}

// A missing <dir>, a leading-dash first argument and an unknown flag are all
// malformed invocations: they must report as usage errors (exit 2) rather than
// as work that failed.
func TestParseFlagsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                      // bare invocation
		{"--non-interactive"},    // flags but no dir
		{"PROJ-1-foo", "--nope"}, // unknown flag
	} {
		_, err := parseFlags(args)
		if err == nil {
			t.Errorf("parseFlags(%v) = nil error; want error", args)
			continue
		}
		if !cli.IsUsage(err) {
			t.Errorf("parseFlags(%v) err = %v; want a usage error", args, err)
		}
	}
}

func TestParseFlagsNoFetch(t *testing.T) {
	o, err := parseFlags([]string{"PROJ-1-foo", "--no-fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.noFetch {
		t.Errorf("opts = %+v", o)
	}
}

func TestParseFlagsRemoveAll(t *testing.T) {
	o, err := parseFlags([]string{"PROJ-1-foo", "--remove-all"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.removeAll {
		t.Errorf("opts = %+v", o)
	}
}

// TestRunHelp: -h/--help is answered before any argument checking, prints the
// usage block to stdout and exits 0 - even though a bare `done` needs <dir>.
func TestRunHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var code int
		out := captureStdout(t, func() { code = Run([]string{flag}) })
		if code != cli.OK {
			t.Fatalf("Run(%s) = %d; want %d", flag, code, cli.OK)
		}
		for _, want := range []string{
			"usage: git work done <dir>",
			"--delete-local", "--delete-remote", "--force",
			"--remove-all", "--no-fetch", "--non-interactive", "-h, --help",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("help output = %q; want %q", out, want)
			}
		}
	}
}

// TestRunUsageExitCode: a malformed invocation exits 2, distinguishable from
// work that failed.
func TestRunUsageExitCode(t *testing.T) {
	if code := Run(nil); code != cli.Usage {
		t.Errorf("Run(nil) = %d; want %d", code, cli.Usage)
	}
}

func TestExtraFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{".git-work.yaml", ".git-work.yaml.tmp", "TICKET.md", "README.md", "CLAUDE.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A repo worktree dir with contents; skipped wholesale via st.Repos.
	if err := os.MkdirAll(filepath.Join(dir, "alpha", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "alpha", "code.go"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "alpha", "sub", "more.go"), []byte("x\n"), 0o644)

	st := &state.State{Repos: []state.Repo{{Name: "alpha"}}}
	got, err := extraFiles(dir, st)
	if err != nil || len(got) != 0 {
		t.Errorf("clean folder: extraFiles = %v, %v; want none", got, err)
	}

	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "keep.txt"), []byte("x\n"), 0o644)
	// a generated name below the root is NOT allowlisted
	os.WriteFile(filepath.Join(dir, "sub", "README.md"), []byte("x\n"), 0o644)

	got, err = extraFiles(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"notes.txt", filepath.Join("sub", "README.md"), filepath.Join("sub", "keep.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extraFiles = %v, want %v", got, want)
	}
}

// writeWorkFolder creates workDir with a zero-repo state file plus any stray
// files listed, and points config at an isolated (empty) XDG dir so config.Load
// returns harmless defaults. Zero repos keeps run()'s teardown loop a no-op, so
// the leftover-decision flow is exercised without a git fixture.
func writeWorkFolder(t *testing.T, strays ...string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "PROJ-1-work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := &state.State{TicketID: "PROJ-1", Title: "work"}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}
	for _, f := range strays {
		if err := os.WriteFile(filepath.Join(workDir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workDir
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDoneRemoveAllRemovesFolder: a non-interactive done with a stray file and
// --remove-all removes the whole folder.
func TestDoneRemoveAllRemovesFolder(t *testing.T) {
	workDir := writeWorkFolder(t, ".env")
	var err error
	_ = captureStdout(t, func() {
		err = run([]string{workDir, "--non-interactive", "--remove-all"})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("work folder still present (%v); --remove-all should delete it", statErr)
	}
}

// TestDoneKeepsFolderWithLeftover: a non-interactive done with a stray file and
// WITHOUT --remove-all (the Keep path) succeeds (exit 0), keeps the folder, the
// stray file, and an honest zero-repo state file, and prints the kept list.
func TestDoneKeepsFolderWithLeftover(t *testing.T) {
	workDir := writeWorkFolder(t, ".env")
	var err error
	out := captureStdout(t, func() {
		err = run([]string{workDir, "--non-interactive"})
	})
	if err != nil {
		t.Fatalf("run: %v; Keep path must succeed", err)
	}
	if _, statErr := os.Stat(workDir); statErr != nil {
		t.Errorf("work folder missing (%v); Keep must leave it", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, ".env")); statErr != nil {
		t.Errorf("stray .env missing (%v); Keep must leave user files", statErr)
	}
	reloaded, loadErr := state.Load(workDir)
	if loadErr != nil {
		t.Fatalf("reload state: %v; Keep must leave an honest state file", loadErr)
	}
	if len(reloaded.Repos) != 0 {
		t.Errorf("reloaded repos = %v; want zero-repo honest state", reloaded.Repos)
	}
	if !strings.Contains(out, ".env") {
		t.Errorf("output = %q; want the kept-files list mentioning .env", out)
	}
}

// TestDoneRemovesCleanFolder: with no stray files the folder is removed even
// without --remove-all (nothing to keep).
func TestDoneRemovesCleanFolder(t *testing.T) {
	workDir := writeWorkFolder(t)
	var err error
	_ = captureStdout(t, func() {
		err = run([]string{workDir, "--non-interactive"})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("clean work folder still present (%v); should be removed", statErr)
	}
}
