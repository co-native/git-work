package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/co-native/git-work/internal/layout"
)

// Integration routes: how a repo's work is meant to reach its default branch.
const (
	// IntegrationLocal lets git-work move the commits itself (`integrate`).
	IntegrationLocal = "local"
	// IntegrationPR means the route is a pull request, so `integrate` refuses.
	IntegrationPR = "pr"
)

// IntegrationRoutes lists the accepted values of the `integration` key.
var IntegrationRoutes = []string{IntegrationLocal, IntegrationPR}

// Defaults are the house rules a repo inherits when it says nothing.
//
// Policy fields are string enums rather than bools so that "" can mean
// "unset". A bool cannot distinguish false from absent, which is what a
// per-repo override needs: the house rule may be either value, and a repo has
// to be able to override it in either direction. A *bool would do it too, at
// the cost of the only pointer field in this config.
type Defaults struct {
	Integration string `yaml:"integration,omitempty" json:"integration,omitempty"`
}

// RepoConfig overrides Defaults for one repo. Fields shared with Defaults use
// the same "" == inherit convention; fields that exist only here (AddByDefault)
// have no house rule to inherit, so a plain bool is enough.
type RepoConfig struct {
	AddByDefault bool   `yaml:"add_by_default,omitempty" json:"add_by_default,omitempty"`
	Integration  string `yaml:"integration,omitempty" json:"integration,omitempty"`
}

// ValidRoute reports whether s is an accepted `integration` value. The empty
// string is valid: it is how a level says "inherit".
func ValidRoute(s string) bool {
	return s == "" || s == IntegrationLocal || s == IntegrationPR
}

// Validate checks the schema rules this package owns. Like provider.Validate
// it runs at the point of use rather than at load, so a config file that is
// only partly relevant to the command at hand cannot fail it.
func (c *Config) Validate() error {
	if !ValidRoute(c.Defaults.Integration) {
		return fmt.Errorf("defaults.integration: invalid route %q (want %s)",
			c.Defaults.Integration, strings.Join(IntegrationRoutes, " or "))
	}
	for name, r := range c.Repos {
		if !ValidRoute(r.Integration) {
			return fmt.Errorf("repos.%s.integration: invalid route %q (want %s)",
				name, r.Integration, strings.Join(IntegrationRoutes, " or "))
		}
	}
	return nil
}

// Integration resolves the route for a repo: per-repo override, else the
// house default, else local - git-work's behavior before this key existed, so
// a config that never mentions it keeps working exactly as it did.
func (c *Config) Integration(repo string) string {
	if r, ok := c.Repos[repo]; ok && r.Integration != "" {
		return r.Integration
	}
	if c.Defaults.Integration != "" {
		return c.Defaults.Integration
	}
	return IntegrationLocal
}

// Pattern matches a ticket id for a provider. Exactly one of Prefix/Regex is
// set. Repo is required for github providers and forbidden for jira.
type Pattern struct {
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex  string `yaml:"regex,omitempty" json:"regex,omitempty"`
	Repo   string `yaml:"repo,omitempty" json:"repo,omitempty"`
}

// Provider describes a built-in ticket source (jira or github).
type Provider struct {
	Name       string    `yaml:"name" json:"name"`
	Type       string    `yaml:"type" json:"type"`
	Default    bool      `yaml:"default,omitempty" json:"default,omitempty"`
	FolderCase string    `yaml:"folder_case,omitempty" json:"folder_case,omitempty"`
	BranchCase string    `yaml:"branch_case,omitempty" json:"branch_case,omitempty"`
	Patterns   []Pattern `yaml:"patterns,omitempty" json:"patterns,omitempty"`
}

// Paths holds the on-disk locations git-work operates on.
type Paths struct {
	Repos string `yaml:"repos" json:"repos"`
	Work  string `yaml:"work" json:"work"`
}

// Config is the global git-work configuration.
//
// Providers is a list and Repos is a map, deliberately. Provider order is
// semantic - provider.Resolve walks the list and the first pattern that
// matches an id wins - so a map would make resolution depend on an arbitrary
// key order. A repo config is a keyed lookup with no precedence between
// entries, so the map both matches how the dot-path keys already address them
// (`repos.<name>.<field>`, never by index) and makes duplicate names
// unrepresentable rather than silently shadowed.
type Config struct {
	Defaults  Defaults              `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Paths     Paths                 `yaml:"paths" json:"paths"`
	Providers []Provider            `yaml:"ticket_providers,omitempty" json:"ticket_providers,omitempty"`
	Repos     map[string]RepoConfig `yaml:"repos,omitempty" json:"repos,omitempty"`
}

func defaults() *Config {
	// Written in ~ form rather than resolved: these values are what `config
	// init` writes to disk and what `config list` shows, and a config that
	// says ~/dev/repos survives being synced to a machine with a different
	// home directory. Resolution happens in Layout, at the point of use.
	return &Config{
		Paths: Paths{
			Repos: filepath.Join("~", "dev", "repos"),
			Work:  filepath.Join("~", "dev", "work"),
		},
	}
}

// expandPath resolves a configured path. A leading `~` or `~/` becomes the home
// directory, and `${VAR}`/`$VAR` are read from the environment. key names the
// setting for error messages.
//
// An undefined variable is an error, never an empty expansion: os.ExpandEnv
// would turn a mistyped ${HOEM}/dev/repos into /dev/repos - an absolute path
// rooted at / that looks plausible and is silently wrong. That is the exact
// class of bug this function exists to prevent, so it fails loudly instead.
//
// Expansion is a single pass in a fixed order - `~` against the literal the
// operator wrote, then variables - so a variable whose value contains `~` or
// `$` is not rescanned and cannot expand a second time.
func expandPath(key, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "~") {
		rest := s[1:]
		// os.IsPathSeparator accepts `\` as well as `/` on Windows and only `/`
		// elsewhere, so `~\dev\repos` works there without `~weird\name` becoming
		// a path on Unix, where a backslash is a legal filename character.
		if rest != "" && !os.IsPathSeparator(rest[0]) {
			return "", fmt.Errorf("%s: ~user paths are not supported (%q); use an absolute path or ${HOME}", key, s)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s: cannot expand %q: %w", key, s, err)
		}
		s = home + rest
	}
	var missing []string
	out := os.Expand(s, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s: undefined variable %s in %q; wrap the name in braces (${NAME}) when it is followed by more text",
			key, strings.Join(missing, ", "), s)
	}
	// Clean to a native path. Concatenating an expanded prefix with the rest of
	// the literal otherwise leaves the operator's separators in place, so
	// `~/dev/repos` on Windows resolves to `C:\Users\me/dev/repos` - accepted by
	// the OS, but it then propagates into every path built from it and into
	// every error message that names one.
	return filepath.Clean(out), nil
}

// Layout resolves the configured roots into a layout.Layout, expanding `~` and
// environment variables. It is the single place configured paths become real
// directories, so every command resolves them identically and the file on disk
// keeps whatever the operator wrote.
func (c *Config) Layout() (layout.Layout, error) {
	repos, err := expandPath("paths.repos", c.Paths.Repos)
	if err != nil {
		return layout.Layout{}, err
	}
	work, err := expandPath("paths.work", c.Paths.Work)
	if err != nil {
		return layout.Layout{}, err
	}
	return layout.Layout{ReposRoot: repos, WorkRoot: work}, nil
}

// Default returns a fresh config populated with built-in defaults.
func Default() *Config { return defaults() }

// DefaultPath returns $XDG_CONFIG_HOME/git-work/config.yaml (or ~/.config/...).
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "git-work", "config.yaml")
}

// OverlayPath returns the optional CLAUDE.md beside the config file, whose
// contents are inlined into every work folder's generated CLAUDE.md. It sits in
// git-work's own config directory rather than under the work root because it is
// configuration, not working data, and because the work root is a directory
// git-work creates and destroys folders inside.
func OverlayPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "CLAUDE.md")
}

// Load reads the default config path.
func Load() (*Config, error) { return LoadFrom(DefaultPath()) }

// LoadFrom reads a config file. Missing file -> defaults (no error). Format is
// chosen by extension (.json = JSON, else YAML).
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults(), nil
		}
		return nil, err
	}
	c := defaults()
	if strings.EqualFold(filepath.Ext(path), ".json") {
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else {
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return c, nil
}

// SaveTo writes the config (YAML unless the path ends in .json), creating parent dirs.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var data []byte
	var err error
	if strings.EqualFold(filepath.Ext(path), ".json") {
		data, err = json.MarshalIndent(c, "", "  ")
	} else {
		data, err = yaml.Marshal(c)
	}
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
