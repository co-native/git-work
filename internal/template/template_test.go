package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestAggregateClaude(t *testing.T) {
	repos := t.TempDir()
	// repo "alpha" has an overlay; "beta" does not
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	os.WriteFile(filepath.Join(repos, "alpha", "CLAUDE.md"), []byte("alpha guidance\n"), 0o644)
	os.MkdirAll(filepath.Join(repos, "beta"), 0o755)

	// beta reuses an existing branch, so its section must state that branch,
	// not the folder-level work branch.
	block := AggregateClaude(ClaudeInput{
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
func TestAggregateClaudeBranchFallback(t *testing.T) {
	block := AggregateClaude(ClaudeInput{
		ReposRoot: t.TempDir(), TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	})
	if !strings.Contains(block, "## alpha (./alpha)\nBranch: PROJ-1-foo\n") {
		t.Errorf("alpha section should fall back to the work branch:\n%s", block)
	}
}

// An empty ticket id (ticketless work folder) renders the title alone -
// no "id: " prefix and no dangling colon.
func TestAggregateClaudeTicketless(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)

	block := AggregateClaude(ClaudeInput{
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
func TestAggregateClaudeEmbedsWorkFolderDoc(t *testing.T) {
	block := AggregateClaude(ClaudeInput{
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
func TestAggregateClaudeOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "CLAUDE.md")

	in := ClaudeInput{
		ReposRoot: t.TempDir(), OverlayPath: overlay,
		TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	}

	t.Run("absent", func(t *testing.T) {
		if got := AggregateClaude(in); strings.Contains(got, "operator guidance") {
			t.Errorf("nothing should be inlined when the overlay is missing:\n%s", got)
		}
	})

	t.Run("present", func(t *testing.T) {
		// No trailing newline, to prove sections still abut cleanly.
		os.WriteFile(overlay, []byte("## Helper tools\n\noperator guidance"), 0o644)
		got := AggregateClaude(in)
		if !strings.Contains(got, "operator guidance") {
			t.Errorf("overlay not inlined:\n%s", got)
		}
		if strings.Contains(got, "operator guidance## alpha") {
			t.Errorf("overlay ran into the next section:\n%s", got)
		}
	})

	t.Run("empty file is skipped", func(t *testing.T) {
		os.WriteFile(overlay, []byte("   \n\n"), 0o644)
		if got := AggregateClaude(in); strings.Contains(got, "\n\n\n\n") {
			t.Errorf("a blank overlay should add nothing:\n%s", got)
		}
	})
}

// Order is part of the contract: identity, then general rules, then repos.
func TestAggregateClaudeSectionOrder(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	dir := t.TempDir()
	overlay := filepath.Join(dir, "CLAUDE.md")
	os.WriteFile(overlay, []byte("## Helper tools\n"), 0o644)

	got := AggregateClaude(ClaudeInput{
		ReposRoot: repos, OverlayPath: overlay,
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
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	in := ClaudeInput{
		ReposRoot: t.TempDir(), TicketID: "PROJ-1", Title: "Fix login", Branch: "PROJ-1-foo",
		Repos: []RepoRef{{Name: "alpha"}},
	}
	for i := 0; i < 3; i++ {
		if err := WriteManaged(path, AggregateClaude(in)); err != nil {
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
func TestAggregateClaudeNormalisesOverlaySpacing(t *testing.T) {
	repos := t.TempDir()
	os.MkdirAll(filepath.Join(repos, "alpha"), 0o755)
	dir := t.TempDir()
	overlay := filepath.Join(dir, "CLAUDE.md")

	var seen []string
	for _, tail := range []string{"", "\n", "\n\n", "\n\n\n"} {
		os.WriteFile(overlay, []byte("## Helper tools\n\nguidance"+tail), 0o644)
		got := AggregateClaude(ClaudeInput{
			ReposRoot: repos, OverlayPath: overlay,
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
