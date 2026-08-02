package config

import (
	"strings"
	"testing"

	cfg "github.com/co-native/git-work/internal/config"
)

func baseConfig() *cfg.Config {
	return &cfg.Config{
		Paths: cfg.Paths{Repos: "/r", Work: "/w"},
		Providers: []cfg.Provider{
			{Name: "jira", Type: "jira", Default: true, Patterns: []cfg.Pattern{{Prefix: "PROJ"}}},
			{Name: "gh", Type: "github", Patterns: []cfg.Pattern{{Prefix: "dev-tools", Repo: "o/api"}}},
		},
	}
}

func TestGetScalar(t *testing.T) {
	c := baseConfig()
	v, err := getKey(c, "paths.repos")
	if err != nil || v != "/r" {
		t.Fatalf("got %q err %v", v, err)
	}
}

func TestGetUnknownKey(t *testing.T) {
	if _, err := getKey(baseConfig(), "paths.nope"); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestSetScalar(t *testing.T) {
	c := baseConfig()
	if err := setKey(c, "paths.work", []string{"/new"}); err != nil {
		t.Fatal(err)
	}
	if c.Paths.Work != "/new" {
		t.Fatalf("work = %q", c.Paths.Work)
	}
}

func TestSetScalarTooManyValues(t *testing.T) {
	if err := setKey(baseConfig(), "paths.work", []string{"/a", "/b"}); err == nil {
		t.Fatal("expected error for >1 value on a scalar")
	}
}

func TestUnsetScalarResetsDefault(t *testing.T) {
	c := baseConfig()
	if err := unsetKey(c, "paths.repos"); err != nil {
		t.Fatal(err)
	}
	if c.Paths.Repos != cfg.Default().Paths.Repos {
		t.Fatalf("repos = %q, want default", c.Paths.Repos)
	}
}

func TestUnsetUnknownKey(t *testing.T) {
	if err := unsetKey(baseConfig(), "paths.nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetProviderScalars(t *testing.T) {
	c := baseConfig()
	if v, _ := getKey(c, "providers.jira.type"); v != "jira" {
		t.Errorf("type = %q", v)
	}
	if v, _ := getKey(c, "providers.jira.default"); v != "true" {
		t.Errorf("default = %q", v)
	}
}

func TestGetProviderPatterns(t *testing.T) {
	v, err := getKey(baseConfig(), "providers.gh.patterns")
	if err != nil || v != "dev-tools=o/api" {
		t.Fatalf("patterns = %q err=%v", v, err)
	}
}

func TestSetProviderPatterns(t *testing.T) {
	c := baseConfig()
	if err := setKey(c, "providers.gh.patterns", []string{"dev-tools=o/api", "acme=o/acme"}); err != nil {
		t.Fatal(err)
	}
	got := findProvider(c, "gh").Patterns
	if len(got) != 2 || got[1].Prefix != "acme" || got[1].Repo != "o/acme" {
		t.Fatalf("patterns = %#v", got)
	}
}

func TestSetProviderTypeAutocreates(t *testing.T) {
	c := &cfg.Config{}
	if err := setKey(c, "providers.new.type", []string{"github"}); err != nil {
		t.Fatal(err)
	}
	if findProvider(c, "new") == nil {
		t.Fatal("provider not created")
	}
}

func TestSetFolderCase(t *testing.T) {
	c := baseConfig()
	if err := setKey(c, "providers.gh.folder_case", []string{"lower"}); err != nil {
		t.Fatal(err)
	}
	if findProvider(c, "gh").FolderCase != "lower" {
		t.Error("folder_case not set")
	}
}

func TestUnsetProviderField(t *testing.T) {
	c := baseConfig()
	if err := unsetKey(c, "providers.gh.patterns"); err != nil {
		t.Fatal(err)
	}
	if findProvider(c, "gh").Patterns != nil {
		t.Error("patterns not cleared")
	}
}

func TestGetWholeProvider(t *testing.T) {
	v, err := getKey(baseConfig(), "providers.gh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(v, "type: github") {
		t.Errorf("expected YAML with 'type: github', got:\n%s", v)
	}
	if !strings.Contains(v, "name: gh") {
		t.Errorf("expected YAML with 'name: gh', got:\n%s", v)
	}
}

// TestSetRepoKeyOnNilMap pins the nil-map case: Load leaves Repos nil for a
// config with no `repos:` block, and assigning into a nil map panics.
func TestSetRepoKeyOnNilMap(t *testing.T) {
	c := baseConfig()
	if c.Repos != nil {
		t.Fatal("baseConfig should start with a nil Repos map")
	}
	if err := setKey(c, "repos.api.integration", []string{"pr"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Repos["api"].Integration; got != cfg.IntegrationPR {
		t.Fatalf("integration = %q, want %q", got, cfg.IntegrationPR)
	}
}

// TestSetRepoKeyPreservesSiblings guards the read-modify-write: setting one
// field must not blank the others, which is the failure mode a map's
// non-addressable elements invite.
func TestSetRepoKeyPreservesSiblings(t *testing.T) {
	c := baseConfig()
	if err := setKey(c, "repos.demo.add_by_default", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if err := setKey(c, "repos.demo.integration", []string{"pr"}); err != nil {
		t.Fatal(err)
	}
	r := c.Repos["demo"]
	if !r.AddByDefault || r.Integration != cfg.IntegrationPR {
		t.Fatalf("repo config = %+v; want both fields set", r)
	}
}

func TestSetRepoInvalidRoute(t *testing.T) {
	c := baseConfig()
	if err := setKey(c, "repos.demo.integration", []string{"bogus"}); err == nil {
		t.Fatal("expected error for an invalid route")
	}
	if _, ok := c.Repos["demo"]; ok {
		t.Fatal("a rejected set must not create the entry")
	}
}

// TestSetIntegrationEmptyRejected: "" means inherit in the struct, but
// clearing a key is `unset`'s job, not `set ""`.
func TestSetIntegrationEmptyRejected(t *testing.T) {
	if err := setKey(baseConfig(), "defaults.integration", []string{""}); err == nil {
		t.Fatal("expected error for an empty route")
	}
}

func TestUnsetRepoFieldAndWholeEntry(t *testing.T) {
	c := baseConfig()
	for _, k := range []string{"repos.demo.add_by_default", "repos.demo.integration"} {
		v := "true"
		if strings.HasSuffix(k, "integration") {
			v = "pr"
		}
		if err := setKey(c, k, []string{v}); err != nil {
			t.Fatal(err)
		}
	}
	if err := unsetKey(c, "repos.demo.integration"); err != nil {
		t.Fatal(err)
	}
	if r := c.Repos["demo"]; r.Integration != "" || !r.AddByDefault {
		t.Fatalf("after field unset: %+v", r)
	}
	if err := unsetKey(c, "repos.demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Repos["demo"]; ok {
		t.Fatal("unset repos.<name> should drop the entry")
	}
}

func TestUnsetUnknownRepo(t *testing.T) {
	if err := unsetKey(baseConfig(), "repos.nope"); err == nil {
		t.Fatal("expected error for an unknown repo")
	}
}
