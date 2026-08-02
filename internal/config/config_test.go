package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	c, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Paths.Repos == "" || c.Paths.Work == "" {
		t.Errorf("expected defaults, got %+v", c)
	}
}

func TestRoundTripYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := &Config{
		Paths: Paths{Repos: "/r", Work: "/w"},
		Providers: []Provider{{
			Name: "jira", Type: "jira", Default: true,
			Patterns: []Pattern{{Prefix: "PROJ"}},
		}},
	}
	if err := in.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	out, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Paths.Repos != "/r" || len(out.Providers) != 1 || out.Providers[0].Name != "jira" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	if out.Providers[0].Type != "jira" || len(out.Providers[0].Patterns) != 1 || out.Providers[0].Patterns[0].Prefix != "PROJ" {
		t.Errorf("round-trip provider mismatch: %+v", out.Providers[0])
	}
}

func TestLoadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"paths":{"repos":"/r","work":"/w"}}`), 0o644)
	out, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Paths.Repos != "/r" {
		t.Errorf("json load = %+v", out)
	}
}

// TestIntegrationResolution pins the inherit chain: per-repo override, else
// the house rule, else local (git-work's behavior before the key existed).
func TestIntegrationResolution(t *testing.T) {
	cases := []struct {
		name     string
		defaults string
		repos    map[string]RepoConfig
		repo     string
		want     string
	}{
		{"nothing set", "", nil, "api", IntegrationLocal},
		{"house rule only", IntegrationPR, nil, "api", IntegrationPR},
		{"repo overrides permissive house", IntegrationLocal,
			map[string]RepoConfig{"api": {Integration: IntegrationPR}}, "api", IntegrationPR},
		{"repo overrides restrictive house", IntegrationPR,
			map[string]RepoConfig{"api": {Integration: IntegrationLocal}}, "api", IntegrationLocal},
		{"unlisted repo takes the house rule", IntegrationPR,
			map[string]RepoConfig{"api": {Integration: IntegrationLocal}}, "web", IntegrationPR},
		{"listed but silent repo inherits", IntegrationPR,
			map[string]RepoConfig{"api": {AddByDefault: true}}, "api", IntegrationPR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Defaults: Defaults{Integration: tc.defaults}, Repos: tc.repos}
			if got := c.Integration(tc.repo); got != tc.want {
				t.Errorf("Integration(%q) = %q, want %q", tc.repo, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	ok := &Config{Defaults: Defaults{Integration: IntegrationPR},
		Repos: map[string]RepoConfig{"api": {Integration: IntegrationLocal}, "web": {AddByDefault: true}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := (&Config{Defaults: Defaults{Integration: "bogus"}}).Validate(); err == nil {
		t.Error("expected an error for an invalid defaults route")
	}
	if err := (&Config{Repos: map[string]RepoConfig{"api": {Integration: "bogus"}}}).Validate(); err == nil {
		t.Error("expected an error for an invalid repo route")
	}
}

// TestRepoConfigRoundTrip: the repos map survives YAML, and an all-zero
// Defaults is omitted rather than written as an empty block.
func TestRepoConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	in := &Config{
		Paths: Paths{Repos: "/r", Work: "/w"},
		Repos: map[string]RepoConfig{"api": {AddByDefault: true, Integration: IntegrationPR}},
	}
	if err := in.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "defaults:") {
		t.Errorf("an all-zero Defaults should be omitted; got:\n%s", data)
	}
	out, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Repos["api"]; !got.AddByDefault || got.Integration != IntegrationPR {
		t.Errorf("round-trip = %+v", got)
	}
}

func TestLayoutExpandsTilde(t *testing.T) {
	// The exact shape DESIGN.md documents. Before expansion existed this
	// produced a literal relative directory named "~", and the resulting
	// "no repos found under ~/dev/repos" error quoted the path back at the
	// operator so it looked correct while being wrong.
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere.
	t.Setenv("USERPROFILE", home)
	c := &Config{Paths: Paths{Repos: "~/dev/repos", Work: "~/dev/work"}}

	l, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "dev", "repos"); l.ReposRoot != want {
		t.Errorf("ReposRoot = %q; want %q", l.ReposRoot, want)
	}
	if want := filepath.Join(home, "dev", "work"); l.WorkRoot != want {
		t.Errorf("WorkRoot = %q; want %q", l.WorkRoot, want)
	}
	// The stored config is untouched, so a synced file keeps its ~ and
	// `config get` shows what the operator actually wrote.
	if c.Paths.Repos != "~/dev/repos" {
		t.Errorf("c.Paths.Repos = %q; want the raw value preserved", c.Paths.Repos)
	}
}

func TestLayoutExpandsEnvVars(t *testing.T) {
	t.Setenv("GW_ROOT", "/srv/code")
	for _, tc := range []struct{ in, want string }{
		{"${GW_ROOT}/repos", "/srv/code/repos"},
		{"$GW_ROOT/repos", "/srv/code/repos"},
		{"${GW_ROOT}suffix", "/srv/codesuffix"},
	} {
		c := &Config{Paths: Paths{Repos: tc.in, Work: "/w"}}
		l, err := c.Layout()
		if err != nil {
			t.Fatalf("Layout(%q): %v", tc.in, err)
		}
		// Cleaned to a native path, so the separator differs by OS.
		if want := filepath.Clean(tc.want); l.ReposRoot != want {
			t.Errorf("Layout(%q).ReposRoot = %q; want %q", tc.in, l.ReposRoot, want)
		}
	}
}

func TestLayoutUndefinedVariableIsError(t *testing.T) {
	// os.ExpandEnv would make this "/dev/repos" - an absolute path rooted at
	// / that looks plausible and is silently wrong. Failing loudly is the
	// whole point.
	os.Unsetenv("GW_DEFINITELY_UNSET")
	c := &Config{Paths: Paths{Repos: "${GW_DEFINITELY_UNSET}/dev/repos", Work: "/w"}}

	_, err := c.Layout()
	if err == nil {
		t.Fatal("Layout() = nil error; want a failure naming the undefined variable")
	}
	for _, want := range []string{"paths.repos", "GW_DEFINITELY_UNSET", "${NAME}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q; want it to mention %q", err, want)
		}
	}
}

func TestLayoutRejectsTildeUser(t *testing.T) {
	// Passing ~otheruser through untouched is how the original bug behaved.
	c := &Config{Paths: Paths{Repos: "~someone/dev/repos", Work: "/w"}}
	_, err := c.Layout()
	if err == nil || !strings.Contains(err.Error(), "~user") {
		t.Fatalf("Layout() err = %v; want a refusal naming ~user", err)
	}
}

func TestLayoutDoesNotRescanExpansions(t *testing.T) {
	// Single pass: a variable whose value contains ~ or $ must not expand a
	// second time, or a config value could reach somewhere unintended.
	t.Setenv("GW_TRICKY", "~/nested")
	c := &Config{Paths: Paths{Repos: "${GW_TRICKY}/repos", Work: "/w"}}

	l, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Clean("~/nested/repos"); l.ReposRoot != want {
		t.Errorf("ReposRoot = %q; want %q - the ~ left literal after one pass", l.ReposRoot, want)
	}
}

func TestDefaultsUseTildeForm(t *testing.T) {
	// `config init` writes these to disk, so they must survive being synced
	// to a machine with a different home directory.
	d := Default()
	if d.Paths.Repos != filepath.Join("~", "dev", "repos") {
		t.Errorf("default paths.repos = %q; want the ~ form", d.Paths.Repos)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere.
	t.Setenv("USERPROFILE", home)
	l, err := d.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "dev", "repos"); l.ReposRoot != want {
		t.Errorf("resolved default = %q; want %q", l.ReposRoot, want)
	}
}
