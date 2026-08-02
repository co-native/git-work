package provider

import (
	"testing"

	"github.com/co-native/git-work/internal/config"
)

func TestValidateAccepts(t *testing.T) {
	provs := []config.Provider{
		{Name: "jira", Type: "jira", Default: true, Patterns: []config.Pattern{{Prefix: "PROJ"}}},
		{Name: "github", Type: "github", Patterns: []config.Pattern{
			{Prefix: "dev-tools", Repo: "acme/api"},
			{Regex: `^gh-(\d+)$`, Repo: "acme/other"},
		}},
	}
	if err := Validate(provs); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]config.Provider{
		"bad type":              {Name: "x", Type: "gitlab"},
		"github default":        {Name: "x", Type: "github", Default: true},
		"github no repo":        {Name: "x", Type: "github", Patterns: []config.Pattern{{Prefix: "p"}}},
		"jira with repo":        {Name: "x", Type: "jira", Patterns: []config.Pattern{{Prefix: "p", Repo: "o/r"}}},
		"both prefix+regex":     {Name: "x", Type: "jira", Patterns: []config.Pattern{{Prefix: "p", Regex: "^p$"}}},
		"neither":               {Name: "x", Type: "jira", Patterns: []config.Pattern{{}}},
		"bad regex":             {Name: "x", Type: "jira", Patterns: []config.Pattern{{Regex: "("}}},
		"github regex no group": {Name: "x", Type: "github", Patterns: []config.Pattern{{Regex: "^gh-1$", Repo: "o/r"}}},
		"bad case":              {Name: "x", Type: "jira", FolderCase: "sideways"},
	}
	for name, p := range cases {
		if err := Validate([]config.Provider{p}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
