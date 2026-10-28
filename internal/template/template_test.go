package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/state"
)

func agg(t *testing.T, in InstructionsInput) string {
	t.Helper()
	got, err := AggregateInstructions(in)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMergeManagedBlockPreservesOutside(t *testing.T) {
	existing := "<!-- git-work:begin -->\nOLD\n<!-- git-work:end -->\n\n## My notes\nkeep me\n"
	got := MergeManagedBlock(existing, "NEW CONTENT")
	if !strings.Contains(got, "NEW CONTENT") {
		t.Error("new content missing")
	}
	if strings.Contains(got, "OLD") {
		t.Error("old managed content should be replaced")
	}
	if !strings.Contains(got, "keep me") {
		t.Error("content outside markers must be preserved")
	}
}

func TestMergeManagedBlockNoMarkers(t *testing.T) {
	got := MergeManagedBlock("just freeform\n", "NEW")
	if !strings.Contains(got, beginMarker) || !strings.Contains(got, "NEW") {
		t.Errorf("expected managed block to be created, got %q", got)
	}
	if !strings.Contains(got, "just freeform") {
		t.Error("existing freeform content should be preserved")
	}
}

func TestAggregateInstructions(t *testing.T) {
	repos := t.TempDir()
	// repo "alpha" has an overlay; "beta" does not
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	os.WriteFile(filepath.Join(repos, "alpha", "AGENTS.md"), []byte("alpha guidance\n"), 0o644)
	os.MkdirAll(filepath.Join(repos, "beta"), 0o755)

	// beta reuses an existing branch, so its section must state that branch,
	// not the folder-level work branch.
	block := agg(t, InstructionsInput{
		ReposRoot: repos, TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha", Branch: "PROJ-1-foo"}, {Name: "beta", Branch: "feature/PROJ-1"}},
	})
	if !strings.HasPrefix(block, "# PROJ-1: Fix login\n") {
		t.Errorf("ticket heading wrong:\n%s", block)
	}
	if !strings.Contains(block, "## alpha (./alpha)\nBranch: PROJ-1-foo\n") || !strings.Contains(block, "alpha guidance") {
		t.Errorf("alpha section/content missing:\n%s", block)
	}
	if !strings.Contains(block, "## beta (./beta)\nBranch: feature/PROJ-1\n") {
		t.Errorf("beta section must carry beta's own branch:\n%s", block)
	}
}

// A repo with no recorded branch falls back to the folder-level branch.
func TestAggregateInstructionsBranchFallback(t *testing.T) {
	block := agg(t, InstructionsInput{
		ReposRoot: t.TempDir(), TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	})
	if !strings.Contains(block, "## alpha (./alpha)\nBranch: PROJ-1-foo\n") {
		t.Errorf("alpha section should fall back to the work branch:\n%s", block)
	}
}

// An empty ticket id (ticketless work folder) renders the title alone -
// no "id: " prefix and no dangling colon.
func TestAggregateInstructionsTicketless(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)

	block := agg(t, InstructionsInput{
		ReposRoot: repos, Title: "spike harness", Branch: "spike-harness",
		Repos: []RepoRef{{Name: "alpha", Branch: "spike-harness"}},
	})
	if !strings.HasPrefix(block, "# spike harness\nBranch: spike-harness\n") {
		t.Errorf("ticketless heading wrong:\n%s", block)
	}
	if !strings.Contains(block, "## alpha (./alpha)") {
		t.Errorf("alpha section missing:\n%s", block)
	}
}

// git-work's own contract ships in the binary, so it appears in every work
// folder with nothing to install per machine.
func TestAggregateInstructionsEmbedsWorkFolderDoc(t *testing.T) {
	block := agg(t, InstructionsInput{
		ReposRoot: t.TempDir(), TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	})
	for _, want := range []string{"## Work folder", ".scratch/", "git-work:begin"} {
		if !strings.Contains(block, want) {
			t.Errorf("embedded work folder doc missing %q:\n%s", want, block)
		}
	}
}

// The operator overlay is optional: absent is silently fine, present is inlined.
func TestAggregateInstructionsOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "AGENTS.md")

	in := InstructionsInput{
		ReposRoot: t.TempDir(), OverlayDir: dir,
		TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	}

	t.Run("absent", func(t *testing.T) {
		if got := agg(t, in); strings.Contains(got, "operator guidance") {
			t.Errorf("nothing should be inlined when the overlay is missing:\n%s", got)
		}
	})

	t.Run("present", func(t *testing.T) {
		// No trailing newline, to prove sections still abut cleanly.
		os.WriteFile(overlay, []byte("## Helper tools\n\noperator guidance"), 0o644)
		got := agg(t, in)
		if !strings.Contains(got, "operator guidance") {
			t.Errorf("overlay not inlined:\n%s", got)
		}
		if strings.Contains(got, "operator guidance## alpha") {
			t.Errorf("overlay ran into the next section:\n%s", got)
		}
	})

	t.Run("empty file is skipped", func(t *testing.T) {
		os.WriteFile(overlay, []byte("   \n\n"), 0o644)
		if got := agg(t, in); strings.Contains(got, "\n\n\n\n") {
			t.Errorf("a blank overlay should add nothing:\n%s", got)
		}
	})
}

// Order is part of the contract: identity, then general rules, then repos.
func TestAggregateInstructionsSectionOrder(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("## Helper tools\n"), 0o644)

	got := agg(t, InstructionsInput{
		ReposRoot: repos, OverlayDir: dir,
		TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	})

	order := []string{"# PROJ-1: Fix login", "## Work folder", "## Helper tools", "## alpha (./alpha)"}
	at := -1
	for _, s := range order {
		i := strings.Index(got, s)
		if i < 0 {
			t.Fatalf("section %q missing:\n%s", s, got)
		}
		if i < at {
			t.Errorf("section %q is out of order:\n%s", s, got)
		}
		at = i
	}
}

// Regenerating must replace the block, never accumulate - the whole point of
// the markers.
func TestWriteManagedIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	in := InstructionsInput{
		ReposRoot: t.TempDir(), TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	}
	for i := 0; i < 3; i++ {
		if err := WriteManaged(path, agg(t, in)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "## Work folder"); n != 1 {
		t.Errorf("work folder doc appears %d times after 3 writes; want 1:\n%s", n, data)
	}
	if n := strings.Count(string(data), beginMarker); n != 1 {
		t.Errorf("begin marker appears %d times; want 1", n)
	}
}

// Inlined content should not change the block's spacing based on how many
// trailing newlines the source file happens to carry - an assembled overlay
// arrives with two, a hand-written one with none.
func TestAggregateInstructionsNormalisesOverlaySpacing(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	dir := t.TempDir()
	overlay := filepath.Join(dir, "AGENTS.md")

	var seen []string
	for _, tail := range []string{"", "\n", "\n\n", "\n\n\n"} {
		os.WriteFile(overlay, []byte("## Helper tools\n\nguidance"+tail), 0o644)
		got := agg(t, InstructionsInput{
			ReposRoot: repos, OverlayDir: dir,
			TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
			Repos: []RepoRef{{Name: "alpha"}},
		})
		if strings.Contains(got, "\n\n\n") {
			t.Errorf("tail %q left a double blank line:\n%s", tail, got)
		}
		seen = append(seen, got)
	}
	for i, got := range seen {
		if got != seen[0] {
			t.Errorf("output %d differs from the first purely by source trailing newlines", i)
		}
	}
}

// An overlay under the other instructions name is the fallback, so switching
// the setting does not require the overlays to be renamed. When both names
// exist, the one matching the setting wins.
func TestAggregateInstructionsOverlayFallback(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	os.WriteFile(filepath.Join(repos, "alpha", "CLAUDE.md"), []byte("alpha claude\n"), 0o644)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("operator claude\n"), 0o644)

	in := InstructionsInput{
		File: config.InstructionsAgents, ReposRoot: repos, OverlayDir: dir,
		TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	}
	got := agg(t, in)
	for _, want := range []string{"operator claude", "alpha claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("CLAUDE.md overlay should be the fallback for AGENTS.md, missing %q:\n%s", want, got)
		}
	}

	os.WriteFile(filepath.Join(repos, "alpha", "AGENTS.md"), []byte("alpha agents\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("operator agents\n"), 0o644)
	got = agg(t, in)
	for _, want := range []string{"operator agents", "alpha agents"} {
		if !strings.Contains(got, want) {
			t.Errorf("the overlay named like the setting must win, missing %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{"operator claude", "alpha claude"} {
		if strings.Contains(got, stale) {
			t.Errorf("only one overlay per location may be inlined, found %q:\n%s", stale, got)
		}
	}

	in.File = config.InstructionsClaude
	got = agg(t, in)
	if !strings.Contains(got, "operator claude") || !strings.Contains(got, "alpha claude") {
		t.Errorf("with CLAUDE.md configured the CLAUDE.md overlays must win:\n%s", got)
	}
}

// An installer's leading "generated from" comment is not inlined: only
// CLAUDE.md is documented to have block comments stripped by its reader.
func TestAggregateInstructionsStripsLeadingComment(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"),
		[]byte("<!-- generated by make install from: a.md b.md -->\n\n## Helper tools\n\nguidance\n"), 0o644)
	got := agg(t, InstructionsInput{
		ReposRoot: t.TempDir(), OverlayDir: dir,
		TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
	})
	if strings.Contains(got, "<!-- generated") {
		t.Errorf("leading comment should be stripped:\n%s", got)
	}
	if !strings.Contains(got, "\n\n## Helper tools\n\nguidance\n") {
		t.Errorf("content after the comment should be inlined with normal spacing:\n%s", got)
	}
}

func TestStripLeadingComment(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"plain\n":                      "plain\n",
		"<!-- a -->\nbody\n":           "body\n",
		"\n<!-- a\nb -->\n\n\nbody\n":  "body\n",
		"<!-- never closed\nbody\n":    "<!-- never closed\nbody\n",
		"body\n<!-- not leading -->\n": "body\n<!-- not leading -->\n",
	}
	for in, want := range cases {
		if got := stripLeadingComment(in); got != want {
			t.Errorf("stripLeadingComment(%q) = %q, want %q", in, got, want)
		}
	}
}

// Switching instructions_file renames the generated file, so a folder never
// holds both names.
func TestMigrateInstructions(t *testing.T) {
	managed := beginMarker + "\nOLD\n" + endMarker + "\n"

	t.Run("rename keeps hand content", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(managed+"\n## My notes\nkeep me\n"), 0o644)
		if err := MigrateInstructions(dir, config.InstructionsAgents); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
			t.Error("CLAUDE.md should have been renamed away")
		}
		data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "keep me") || !strings.Contains(string(data), beginMarker) {
			t.Errorf("rename should carry the whole file:\n%s", data)
		}
	})

	t.Run("reverse direction", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(managed), 0o644)
		if err := MigrateInstructions(dir, config.InstructionsClaude); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Error("AGENTS.md should have been renamed to CLAUDE.md")
		}
	})

	t.Run("hand-written file is not ours", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("## Mine\n"), 0o644)
		if err := MigrateInstructions(dir, config.InstructionsAgents); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Error("a CLAUDE.md without markers must be left alone")
		}
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
			t.Error("nothing should be created")
		}
	})

	t.Run("both exist, old holds only the block", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(managed), 0o644)
		os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("## Mine\n"), 0o644)
		if err := MigrateInstructions(dir, config.InstructionsAgents); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
			t.Error("an old file that held only the block should be deleted")
		}
		data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if string(data) != "## Mine\n" {
			t.Errorf("the new file is left for the re-render to merge into:\n%s", data)
		}
	})

	t.Run("both exist, old holds hand content", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(managed+"\n## Old notes\nkeep me\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("## Mine\n"), 0o644)
		if err := MigrateInstructions(dir, config.InstructionsAgents); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), beginMarker) || !strings.Contains(string(data), "keep me") {
			t.Errorf("the block should be cut out and the hand content kept:\n%s", data)
		}
	})

	t.Run("nothing to migrate", func(t *testing.T) {
		dir := t.TempDir()
		if err := MigrateInstructions(dir, config.InstructionsAgents); err != nil {
			t.Fatal(err)
		}
	})
}

// WriteWorkFolder writes the configured name and migrates the other, end to
// end: a folder rendered under CLAUDE.md re-rendered under AGENTS.md ends up
// with one file that carries the fresh block and the old hand content.
func TestWriteWorkFolderMigrates(t *testing.T) {
	work := t.TempDir()
	st := &state.State{TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-foo"}}}
	if err := WriteWorkFolder(work, WorkFolderOptions{ReposRoot: t.TempDir(), File: config.InstructionsClaude}, st, nil); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(work, "CLAUDE.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\n## My notes\nkeep me\n")
	f.Close()

	st.Title = "Fix login properly"
	if err := WriteWorkFolder(work, WorkFolderOptions{ReposRoot: t.TempDir(), File: config.InstructionsAgents}, st, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("CLAUDE.md should be gone after re-rendering under AGENTS.md")
	}
	data, err := os.ReadFile(filepath.Join(work, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# PROJ-1: Fix login properly", "keep me"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, data)
		}
	}
	if n := strings.Count(string(data), beginMarker); n != 1 {
		t.Errorf("begin marker appears %d times; want 1", n)
	}
}

// Both instructions names are generated files, so `done` may delete a folder
// written under either setting without --remove-all.
func TestGeneratedFilesCoverBothInstructionsNames(t *testing.T) {
	have := map[string]bool{}
	for _, f := range GeneratedFiles {
		have[f] = true
	}
	for _, f := range config.InstructionsFiles {
		if !have[f] {
			t.Errorf("GeneratedFiles lacks %s", f)
		}
	}
}

func TestAggregateInstructionsModeContract(t *testing.T) {
	worktree := agg(t, InstructionsInput{Title: "T", Branch: "b"})
	if !strings.Contains(worktree, "per-feature container holding one git worktree") {
		t.Errorf("worktree contract missing:\n%s", worktree)
	}
	clone := agg(t, InstructionsInput{Mode: "clone", Title: "T", Branch: "b"})
	if !strings.Contains(clone, "clone-mode work folder") || !strings.Contains(clone, ".worktrees/") {
		t.Errorf("clone contract missing:\n%s", clone)
	}
	if strings.Contains(clone, "per-feature container holding one git worktree") {
		t.Error("clone contract still claims one worktree per repo")
	}
}

func TestRepoOverlaySources(t *testing.T) {
	repos := t.TempDir()
	cfgOverlays := t.TempDir()
	in := InstructionsInput{
		ReposRoot: repos, RepoOverlayDir: cfgOverlays,
		TicketID: "T-1", Title: "T", Branch: "b",
		Repos: []RepoRef{{Name: "alpha"}},
	}
	if err := os.MkdirAll(filepath.Join(repos, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgOverlays, "alpha.md"), []byte("from config dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agg(t, in); !strings.Contains(got, "from config dir") {
		t.Errorf("config-dir overlay not inlined:\n%s", got)
	}

	if err := os.WriteFile(filepath.Join(repos, "alpha", "AGENTS.md"), []byte("from repos root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AggregateInstructions(in); err == nil {
		t.Error("expected an error when both overlay sources exist")
	}
}
