package naming

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Fix login redirect":      "fix-login-redirect",
		"  Trailing & symbols!! ": "trailing-symbols",
		"CamelCase Words":         "camelcase-words",
		"already-a-slug":          "already-a-slug",
		"---":                     "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q want %q", in, got, want)
		}
	}
}

func TestInferTicket(t *testing.T) {
	tests := []struct {
		folder, ticket, slug string
	}{
		{"PROJ-3-git-work-updates", "PROJ-3", "git-work-updates"},
		{"PROJ-123", "PROJ-123", ""},
		{"proj-7-fix-thing", "proj-7", "fix-thing"},
		{"proj2-7-fix", "proj2-7", "fix"},
		// Multi-segment ticket prefixes (the repo's own dev-tools-N form).
		{"dev-tools-12-fix-thing", "dev-tools-12", "fix-thing"},
		{"DEV-TOOLS-1", "DEV-TOOLS-1", ""},
		// No ticket-shaped prefix: not a ticket at all (ticketless).
		{"my-cool-feature", "", ""},
		{"123-4-x", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.folder, func(t *testing.T) {
			ticket, slug := InferTicket(tt.folder)
			if ticket != tt.ticket || slug != tt.slug {
				t.Errorf("InferTicket(%q) = %q, %q; want %q, %q", tt.folder, ticket, slug, tt.ticket, tt.slug)
			}
		})
	}
}

func TestSlugForFolder(t *testing.T) {
	tests := []struct {
		name, folder, ticket, want string
	}{
		{"ticket prefix stripped", "PROJ-1-Fix Login", "PROJ-1", "fix-login"},
		{"case-insensitive prefix", "proj-1-fix", "PROJ-1", "fix"},
		{"no prefix", "Fix Login", "PROJ-1", "fix-login"},
		{"lookalike prefix", "PROJ-10-fix", "PROJ-1", "proj-10-fix"},
		{"ticketless", "Spike Harness", "", "spike-harness"},
		{"ticket alone", "PROJ-1", "PROJ-1", "proj-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SlugForFolder(tt.folder, tt.ticket); got != tt.want {
				t.Errorf("SlugForFolder(%q, %q) = %q, want %q", tt.folder, tt.ticket, got, tt.want)
			}
		})
	}
}

func TestBranchForRename(t *testing.T) {
	tests := []struct {
		name                        string
		oldBranch, oldSlug, newSlug string
		want                        string
		ok                          bool
	}{
		{"ticket branch", "PROJ-1-add-auth", "add-auth", "add-login", "PROJ-1-add-login", true},
		{"ticketless branch", "spike-harness", "spike-harness", "spike-otel", "spike-otel", true},
		{"multi segment slug", "PROJ-1-fix-auth", "fix-auth", "fix-login", "PROJ-1-fix-login", true},
		{"prefix casing preserved", "proj-1-fix-auth", "fix-auth", "fix-login", "proj-1-fix-login", true},
		{"hand named branch", "feature/PROJ-1", "add-auth", "add-login", "", false},
		{"hand cased slug", "proj-1-Fix-Auth", "fix-auth", "fix-login", "", false},
		{"slug only in the middle", "PROJ-1-add-auth-x", "add-auth", "add-login", "", false},
		{"empty new slug", "PROJ-1-add-auth", "add-auth", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := BranchForRename(tt.oldBranch, tt.oldSlug, tt.newSlug)
			if got != tt.want || ok != tt.ok {
				t.Errorf("BranchForRename(%q, %q, %q) = %q, %v; want %q, %v",
					tt.oldBranch, tt.oldSlug, tt.newSlug, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFolderNameProblem(t *testing.T) {
	for _, name := range []string{"PROJ-1-fix", "spike harness", ".hidden"} {
		if got := FolderNameProblem(name); got != "" {
			t.Errorf("FolderNameProblem(%q) = %q, want accepted", name, got)
		}
	}
	for _, name := range []string{"", ".", "..", ".worktrees", "-fix", "a/b", `a\b`} {
		if got := FolderNameProblem(name); got == "" {
			t.Errorf("FolderNameProblem(%q) = accepted, want a refusal", name)
		}
	}
}
