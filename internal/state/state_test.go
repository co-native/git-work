package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		TicketID: "PROJ-1", Title: "Fix login", Slug: "fix-login",
		Branch: "PROJ-1-fix-login", Provider: "jira",
		Repos: []Repo{{Name: "myrepo", BranchSource: "new"}},
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.TicketID != "PROJ-1" || len(out.Repos) != 1 || out.Repos[0].Name != "myrepo" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		name string
		st   State
		want string
	}{
		{"ticket", State{TicketID: "PROJ-1", Title: "Fix login"}, "PROJ-1"},
		{"ticketless", State{Title: "spike harness", NoTicket: true}, "spike harness"},
	}
	for _, tt := range cases {
		if got := tt.st.DisplayName(); got != tt.want {
			t.Errorf("%s: DisplayName() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNoTicketRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Title: "spike harness", Slug: "spike-harness", Branch: "spike-harness",
		NoTicket: true,
		Repos:    []Repo{{Name: "a", BranchSource: "new", Branch: "spike-harness"}},
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !out.NoTicket || out.TicketID != "" || out.Title != "spike harness" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestFindUpFromSubdir(t *testing.T) {
	root := t.TempDir()
	s := &State{TicketID: "PROJ-2", Branch: "b"}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "myrepo", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	found, dir, err := FindUp(sub)
	if err != nil {
		t.Fatal(err)
	}
	if dir != root || found.TicketID != "PROJ-2" {
		t.Errorf("FindUp = %q %+v", dir, found)
	}
}

func TestFindUpNotFound(t *testing.T) {
	_, _, err := FindUp(t.TempDir())
	if err == nil {
		t.Error("expected error when no state file found")
	}
}

func TestRepoBranchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		TicketID: "PROJ-1", Branch: "PROJ-1-fix",
		Repos: []Repo{{Name: "a", BranchSource: "existing", Branch: "feature/PROJ-1"}},
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Repos[0].Branch != "feature/PROJ-1" {
		t.Errorf("Repo.Branch = %q, want feature/PROJ-1", out.Repos[0].Branch)
	}
}

func TestRepoBranchMigrationYAML(t *testing.T) {
	dir := t.TempDir()
	old := "ticket_id: PROJ-2\nbranch: PROJ-2-fix\nrepos:\n  - name: a\n    branch_source: new\n  - name: b\n    branch_source: existing\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Repos {
		if r.Branch != "PROJ-2-fix" {
			t.Errorf("%s: Branch = %q, want fallback PROJ-2-fix", r.Name, r.Branch)
		}
	}
}

func TestRemoveRepoExisting(t *testing.T) {
	s := &State{
		Repos: []Repo{
			{Name: "a", BranchSource: "new"},
			{Name: "b", BranchSource: "new"},
			{Name: "c", BranchSource: "new"},
		},
	}
	if ok := s.RemoveRepo("b"); !ok {
		t.Fatal("RemoveRepo(\"b\") = false, want true")
	}
	want := []Repo{{Name: "a", BranchSource: "new"}, {Name: "c", BranchSource: "new"}}
	if len(s.Repos) != len(want) || s.Repos[0].Name != want[0].Name || s.Repos[1].Name != want[1].Name {
		t.Errorf("Repos after RemoveRepo = %+v, want %+v", s.Repos, want)
	}
}

func TestRemoveRepoNotFound(t *testing.T) {
	orig := []Repo{{Name: "a", BranchSource: "new"}, {Name: "b", BranchSource: "new"}}
	s := &State{Repos: append([]Repo(nil), orig...)}
	if ok := s.RemoveRepo("nope"); ok {
		t.Fatal("RemoveRepo(\"nope\") = true, want false")
	}
	if len(s.Repos) != len(orig) || s.Repos[0].Name != orig[0].Name || s.Repos[1].Name != orig[1].Name {
		t.Errorf("Repos after no-op RemoveRepo = %+v, want unchanged %+v", s.Repos, orig)
	}
}

func TestRepoBranchMigrationJSON(t *testing.T) {
	dir := t.TempDir()
	old := `{"ticket_id":"PROJ-3","branch":"PROJ-3-x","repos":[{"name":"a","branch_source":"new"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".git-work.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Repos[0].Branch != "PROJ-3-x" {
		t.Errorf("Repo.Branch = %q, want PROJ-3-x", out.Repos[0].Branch)
	}
}
