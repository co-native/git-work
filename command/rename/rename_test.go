package rename

import (
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/state"
)

func TestCommandDescriptor(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

func TestParseFlags(t *testing.T) {
	t.Run("new name", func(t *testing.T) {
		o, err := parseFlags([]string{"PROJ-1-old", "PROJ-1-new"})
		if err != nil {
			t.Fatal(err)
		}
		if o.dir != "PROJ-1-old" || o.newName != "PROJ-1-new" {
			t.Errorf("opts = %+v", o)
		}
	})
	t.Run("flags after the name", func(t *testing.T) {
		o, err := parseFlags([]string{"PROJ-1-old", "PROJ-1-new", "--keep-branch", "--dry-run"})
		if err != nil {
			t.Fatal(err)
		}
		if !o.keepBranch || !o.dryRun {
			t.Errorf("opts = %+v", o)
		}
	})
	t.Run("name after flags", func(t *testing.T) {
		o, err := parseFlags([]string{"PROJ-1-old", "--ticket", "PROJ-2", "PROJ-2-new"})
		if err != nil {
			t.Fatal(err)
		}
		if o.ticket != "PROJ-2" || o.newName != "PROJ-2-new" {
			t.Errorf("opts = %+v", o)
		}
	})
	t.Run("branch only", func(t *testing.T) {
		o, err := parseFlags([]string{"PROJ-1-old", "--branch", "feature/PROJ-1"})
		if err != nil {
			t.Fatal(err)
		}
		if o.branch != "feature/PROJ-1" || o.newName != "" {
			t.Errorf("opts = %+v", o)
		}
	})
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"no dir", nil, "give a <dir>"},
		{"flag first", []string{"--dry-run"}, "give a <dir>"},
		{"no new name", []string{"PROJ-1-old"}, "at least one new name"},
		{"branch and keep", []string{"PROJ-1-old", "--branch", "x", "--keep-branch"}, "not both"},
		{"remote and keep", []string{"PROJ-1-old", "PROJ-1-new", "--remote", "--keep-branch"}, "needs a branch rename"},
		{"remote ref", []string{"PROJ-1-old", "--branch", "origin/x"}, "origin/ prefix"},
		{"invalid branch", []string{"PROJ-1-old", "--branch", "bad name"}, "not a valid branch name"},
		{"path name", []string{"PROJ-1-old", "a/b"}, "path separator"},
		{"reserved name", []string{"PROJ-1-old", ".worktrees"}, "reserved"},
		{"extra positional", []string{"PROJ-1-old", "one", "two"}, "unexpected argument"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFlags(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseFlags(%q) err = %v, want containing %q", tt.args, err, tt.want)
			}
			if !cli.IsUsage(err) {
				t.Errorf("parseFlags(%q) err = %v; want a usage error", tt.args, err)
			}
		})
	}
}

// planState is a clone-mode work folder: buildPlan resolves each repo's
// directory from state without touching a primary clone, so the derivation can
// be tested without git.
func planState() *state.State {
	return &state.State{
		TicketID: "PROJ-1", Title: "Fix login", Slug: "fix-login",
		Branch: "PROJ-1-fix-login", Mode: layout.ModeClone,
		Repos: []state.Repo{
			{Name: "alpha", BranchSource: "new", Branch: "PROJ-1-fix-login"},
			{Name: "beta", BranchSource: "existing", Branch: "feature/PROJ-1"},
		},
	}
}

func build(t *testing.T, workDir string, st *state.State, o *options, tc *ticketChange) *plan {
	t.Helper()
	p, err := buildPlan(workDir, st, o, tc, layout.Layout{WorkRoot: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanFolderRename(t *testing.T) {
	st := planState()
	p := build(t, "/work/PROJ-1-fix-login", st, &options{newName: "PROJ-1-fix-redirect"}, nil)

	if p.newBase != "PROJ-1-fix-redirect" || p.newSlug != "fix-redirect" {
		t.Errorf("base/slug = %q/%q", p.newBase, p.newSlug)
	}
	if p.newBranch != "PROJ-1-fix-redirect" || !p.branchChanged {
		t.Errorf("branch = %q, changed = %v", p.newBranch, p.branchChanged)
	}
	if !p.movesFolder {
		t.Error("movesFolder = false, want true")
	}
	if got := p.repos[0]; got.action != repoRename || got.to != "PROJ-1-fix-redirect" {
		t.Errorf("alpha = %+v, want a rename", got)
	}
	if got := p.repos[1]; got.action != repoLeave {
		t.Errorf("beta = %+v, want left alone", got)
	}
}

func TestPlanKeepBranch(t *testing.T) {
	st := planState()
	p := build(t, "/work/PROJ-1-fix-login", st, &options{newName: "PROJ-1-fix-redirect", keepBranch: true}, nil)

	if p.newBranch != st.Branch || p.branchChanged {
		t.Errorf("branch = %q, changed = %v; want %q unchanged", p.newBranch, p.branchChanged, st.Branch)
	}
	for _, r := range p.repos {
		if r.action != repoKeep {
			t.Errorf("%s action = %v, want repoKeep", r.name, r.action)
		}
	}
}

func TestPlanBranchOnly(t *testing.T) {
	st := planState()
	p := build(t, "/work/PROJ-1-fix-login", st, &options{branch: "feature/PROJ-1-x"}, nil)

	if p.movesFolder || p.newBase != "PROJ-1-fix-login" || p.newSlug != "fix-login" {
		t.Errorf("folder/slug = %q/%q, moves = %v", p.newBase, p.newSlug, p.movesFolder)
	}
	if p.newBranch != "feature/PROJ-1-x" || !p.branchChanged {
		t.Errorf("branch = %q, changed = %v", p.newBranch, p.branchChanged)
	}
}

func TestPlanTicketlessRename(t *testing.T) {
	st := &state.State{
		Title: "spike harness", Slug: "spike-harness", Branch: "spike-harness",
		NoTicket: true, Mode: layout.ModeClone,
		Repos: []state.Repo{{Name: "alpha", BranchSource: "new", Branch: "spike-harness"}},
	}
	p := build(t, "/work/spike-harness", st, &options{newName: "spike-otel"}, nil)

	if p.newBase != "spike-otel" || p.newSlug != "spike-otel" || p.newBranch != "spike-otel" {
		t.Errorf("base/slug/branch = %q/%q/%q", p.newBase, p.newSlug, p.newBranch)
	}
}

func TestPlanAddsTicket(t *testing.T) {
	st := &state.State{
		Title: "spike harness", Slug: "spike-harness", Branch: "spike-harness",
		NoTicket: true, Mode: layout.ModeClone,
		Repos: []state.Repo{{Name: "alpha", BranchSource: "new", Branch: "spike-harness"}},
	}
	tc := &ticketChange{DisplayID: "PROJ-8", Title: "Make it real", FolderCase: "lower", BranchCase: "upper"}
	p := build(t, "/work/spike-harness", st, &options{ticket: "PROJ-8"}, tc)

	if !p.ticketChanged {
		t.Error("ticketChanged = false, want true")
	}
	if p.newBase != "proj-8-spike-harness" || p.newSlug != "spike-harness" {
		t.Errorf("base/slug = %q/%q", p.newBase, p.newSlug)
	}
	if p.newBranch != "PROJ-8-spike-harness" {
		t.Errorf("branch = %q, want PROJ-8-spike-harness", p.newBranch)
	}
	if got := p.repos[0]; got.action != repoRename || got.to != "PROJ-8-spike-harness" {
		t.Errorf("alpha = %+v, want a rename", got)
	}
}

func TestPlanChangesTicket(t *testing.T) {
	st := planState()
	tc := &ticketChange{DisplayID: "PROJ-2", Title: "Another thing"}
	p := build(t, "/work/PROJ-1-fix-login", st, &options{ticket: "PROJ-2"}, tc)

	if p.newBase != "PROJ-2-fix-login" || p.newBranch != "PROJ-2-fix-login" {
		t.Errorf("base/branch = %q/%q", p.newBase, p.newBranch)
	}
	if !p.ticketChanged || !p.movesFolder {
		t.Errorf("ticketChanged = %v, movesFolder = %v", p.ticketChanged, p.movesFolder)
	}
}

func TestPlanRefusals(t *testing.T) {
	ticketless := &state.State{
		Title: "spike harness", Slug: "spike-harness", Branch: "spike-harness",
		NoTicket: true, Mode: layout.ModeClone,
	}
	for _, tt := range []struct {
		name string
		dir  string
		st   *state.State
		o    *options
		tc   *ticketChange
		want string
	}{
		{
			name: "different ticket prefix", dir: "/work/PROJ-1-fix-login", st: planState(),
			o:    &options{newName: "PROJ-2-fix-login"},
			want: "names ticket",
		},
		{
			name: "ticket prefix on a ticketless folder", dir: "/work/spike-harness", st: ticketless,
			o:    &options{newName: "PROJ-8-spike"},
			want: "names ticket",
		},
		{
			name: "unsuitable ticketless name", dir: "/work/spike-harness", st: ticketless,
			o:    &options{newName: "Spike OTel"},
			want: "named by its slug",
		},
		{
			name: "empty slug", dir: "/work/PROJ-1-fix-login", st: planState(),
			o:    &options{newName: "PROJ-1-!!!"},
			want: "empty slug",
		},
		{
			name: "hand named branch", dir: "/work/PROJ-1-fix", st: &state.State{
				TicketID: "PROJ-1", Slug: "fix", Branch: "feature/PROJ-1",
				Mode: layout.ModeClone,
			},
			o:    &options{newName: "PROJ-1-fix-login"},
			want: "does not follow the folder name",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildPlan(tt.dir, tt.st, tt.o, tt.tc, layout.Layout{WorkRoot: "/work"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildPlan err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestPlanTicketPrefixMatchingNewTicket(t *testing.T) {
	st := planState()
	tc := &ticketChange{DisplayID: "PROJ-2", Title: "Another thing"}
	p := build(t, "/work/PROJ-1-fix-login", st, &options{newName: "PROJ-2-fix-login", ticket: "PROJ-2"}, tc)
	if p.newSlug != "fix-login" {
		t.Errorf("slug = %q, want fix-login", p.newSlug)
	}
}

// A legacy folder whose name carried no slug (adopt can bootstrap `PROJ-123`
// with an empty slug) still takes a branch-only rename.
func TestPlanBranchOnlyWithNoSlug(t *testing.T) {
	st := &state.State{
		TicketID: "PROJ-123", Title: "PROJ-123", Slug: "", Branch: "PROJ-123-work",
		Mode: layout.ModeClone,
	}
	p := build(t, "/work/PROJ-123", st, &options{branch: "feature/PROJ-123"}, nil)
	if p.movesFolder || p.newBranch != "feature/PROJ-123" {
		t.Errorf("movesFolder = %v, branch = %q", p.movesFolder, p.newBranch)
	}
}

func TestPlanCaseOnlyFolderRename(t *testing.T) {
	st := planState()
	p := build(t, "/work/PROJ-1-fix-login", st, &options{newName: "PROJ-1-Fix-Login"}, nil)
	if !p.movesFolder {
		t.Error("movesFolder = false, want true")
	}
	if p.newSlug != "fix-login" || p.branchChanged {
		t.Errorf("slug = %q, branchChanged = %v; want the branch untouched", p.newSlug, p.branchChanged)
	}
}
