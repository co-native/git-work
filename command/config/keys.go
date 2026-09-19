package config

import (
	"fmt"
	"strconv"
	"strings"

	cfg "github.com/co-native/git-work/internal/config"
	"gopkg.in/yaml.v3"
)

// scalar is one fixed dot-path key: how `get`, `set` and `unset` act on it.
// Every scalar takes exactly one value.
type scalar struct {
	get   func(*cfg.Config) string
	set   func(*cfg.Config, string) error
	unset func(*cfg.Config)
}

// scalars are the keys with a fixed name; `repos.<name>.*` and
// `providers.<name>.*` are addressed by entry and live in the field tables.
var scalars = map[string]scalar{
	"paths.repos": {
		get:   func(c *cfg.Config) string { return c.Paths.Repos },
		set:   func(c *cfg.Config, v string) error { c.Paths.Repos = v; return nil },
		unset: func(c *cfg.Config) { c.Paths.Repos = cfg.Default().Paths.Repos },
	},
	"paths.work": {
		get:   func(c *cfg.Config) string { return c.Paths.Work },
		set:   func(c *cfg.Config, v string) error { c.Paths.Work = v; return nil },
		unset: func(c *cfg.Config) { c.Paths.Work = cfg.Default().Paths.Work },
	},
	"defaults.integration": {
		get: func(c *cfg.Config) string { return c.Defaults.Integration },
		set: func(c *cfg.Config, v string) error {
			if err := checkRoute(v); err != nil {
				return err
			}
			c.Defaults.Integration = v
			return nil
		},
		unset: func(c *cfg.Config) { c.Defaults.Integration = "" },
	},
}

// field is one key under a named entry (`repos.<name>.<field>`,
// `providers.<name>.<field>`). set receives the whole config as well as the
// entry, for the one field whose value constrains other entries. multi marks
// a field that takes any number of values; every other field takes one.
type field[T any] struct {
	multi bool
	get   func(*T) string
	set   func(*cfg.Config, *T, []string) error
	unset func(*T)
}

var repoFields = map[string]field[cfg.RepoConfig]{
	"integration": {
		get: func(r *cfg.RepoConfig) string { return r.Integration },
		set: func(_ *cfg.Config, r *cfg.RepoConfig, v []string) error {
			if err := checkRoute(v[0]); err != nil {
				return err
			}
			r.Integration = v[0]
			return nil
		},
		unset: func(r *cfg.RepoConfig) { r.Integration = "" },
	},
	"add_by_default": {
		get: func(r *cfg.RepoConfig) string { return strconv.FormatBool(r.AddByDefault) },
		set: func(_ *cfg.Config, r *cfg.RepoConfig, v []string) error {
			b, err := strconv.ParseBool(v[0])
			if err != nil {
				return fmt.Errorf("add_by_default must be true or false: %w", err)
			}
			r.AddByDefault = b
			return nil
		},
		unset: func(r *cfg.RepoConfig) { r.AddByDefault = false },
	},
}

var providerFields = map[string]field[cfg.Provider]{
	"type": {
		get:   func(p *cfg.Provider) string { return p.Type },
		set:   func(_ *cfg.Config, p *cfg.Provider, v []string) error { p.Type = v[0]; return nil },
		unset: func(p *cfg.Provider) { p.Type = "" },
	},
	"default": {
		get: func(p *cfg.Provider) string { return strconv.FormatBool(p.Default) },
		set: func(c *cfg.Config, p *cfg.Provider, v []string) error {
			b, err := strconv.ParseBool(v[0])
			if err != nil {
				return fmt.Errorf("default must be true or false: %w", err)
			}
			// At most one provider is the default.
			if b {
				for i := range c.Providers {
					c.Providers[i].Default = false
				}
			}
			p.Default = b
			return nil
		},
		unset: func(p *cfg.Provider) { p.Default = false },
	},
	"folder_case": {
		get:   func(p *cfg.Provider) string { return p.FolderCase },
		set:   func(_ *cfg.Config, p *cfg.Provider, v []string) error { p.FolderCase = v[0]; return nil },
		unset: func(p *cfg.Provider) { p.FolderCase = "" },
	},
	"branch_case": {
		get:   func(p *cfg.Provider) string { return p.BranchCase },
		set:   func(_ *cfg.Config, p *cfg.Provider, v []string) error { p.BranchCase = v[0]; return nil },
		unset: func(p *cfg.Provider) { p.BranchCase = "" },
	},
	"patterns": {
		multi: true,
		get: func(p *cfg.Provider) string {
			var lines []string
			for _, pat := range p.Patterns {
				lines = append(lines, patternString(pat))
			}
			return strings.Join(lines, "\n")
		},
		set: func(_ *cfg.Config, p *cfg.Provider, v []string) error {
			var pats []cfg.Pattern
			for _, entry := range v {
				pats = append(pats, parsePatternEntry(entry))
			}
			p.Patterns = pats
			return nil
		},
		unset: func(p *cfg.Provider) { p.Patterns = nil },
	},
}

// splitKey splits a dot-path key into its namespace ("repos"), the entry it
// names, and the field under that entry; each is "" when absent.
func splitKey(key string) (ns, name, field string) {
	ns, rest, _ := strings.Cut(key, ".")
	name, field, _ = strings.Cut(rest, ".")
	return ns, name, field
}

func unknownKey(key string) error { return fmt.Errorf("unknown key %q", key) }

func oneValue(key string) error { return fmt.Errorf("key %q takes exactly one value", key) }

// getKey returns the printable value for a dot-path key. A bare entry
// (`repos.<name>`, `providers.<name>`) prints as YAML.
func getKey(c *cfg.Config, key string) (string, error) {
	if s, ok := scalars[key]; ok {
		return s.get(c), nil
	}
	ns, name, fld := splitKey(key)
	if name == "" {
		return "", unknownKey(key)
	}
	switch ns {
	case "repos":
		r, ok := c.Repos[name]
		if !ok {
			return "", fmt.Errorf("no repo config for %q", name)
		}
		if fld == "" {
			out, err := yaml.Marshal(r)
			return string(out), err
		}
		if f, ok := repoFields[fld]; ok {
			return f.get(&r), nil
		}
	case "providers":
		p := findProvider(c, name)
		if p == nil {
			return "", fmt.Errorf("no provider %q", name)
		}
		if fld == "" {
			out, err := yaml.Marshal(p)
			return string(out), err
		}
		if f, ok := providerFields[fld]; ok {
			return f.get(p), nil
		}
	}
	return "", unknownKey(key)
}

// setKey applies values to a dot-path key. A repo or provider entry that does
// not exist yet is created.
func setKey(c *cfg.Config, key string, values []string) error {
	if s, ok := scalars[key]; ok {
		if len(values) != 1 {
			return oneValue(key)
		}
		return s.set(c, values[0])
	}
	ns, name, fld := splitKey(key)
	if name == "" || fld == "" {
		return unknownKey(key)
	}
	switch ns {
	case "repos":
		f, ok := repoFields[fld]
		if !ok {
			return fmt.Errorf("cannot set %q", key)
		}
		if len(values) != 1 {
			return oneValue(key)
		}
		// Load leaves a nil map for a config with no `repos:` block, and
		// assigning into a nil map panics rather than erroring.
		if c.Repos == nil {
			c.Repos = map[string]cfg.RepoConfig{}
		}
		// Indexing a missing key yields the zero RepoConfig, so this is the
		// auto-create path too; map elements are not addressable, so the
		// mutation is written back.
		r := c.Repos[name]
		if err := f.set(c, &r, values); err != nil {
			return err
		}
		c.Repos[name] = r
		return nil
	case "providers":
		f, ok := providerFields[fld]
		if !ok {
			return fmt.Errorf("cannot set %q", key)
		}
		if !f.multi && len(values) != 1 {
			return oneValue(key)
		}
		p := findProvider(c, name)
		if p == nil {
			c.Providers = append(c.Providers, cfg.Provider{Name: name})
			p = &c.Providers[len(c.Providers)-1]
		}
		return f.set(c, p, values)
	}
	return unknownKey(key)
}

// unsetKey clears a key: a scalar goes back to its default, an entry's field
// to its zero value, and a bare entry (`repos.<name>`, `providers.<name>`) is
// dropped entirely.
func unsetKey(c *cfg.Config, key string) error {
	if s, ok := scalars[key]; ok {
		s.unset(c)
		return nil
	}
	ns, name, fld := splitKey(key)
	if name == "" {
		return unknownKey(key)
	}
	switch ns {
	case "repos":
		r, ok := c.Repos[name]
		if !ok {
			return fmt.Errorf("no repo config for %q", name)
		}
		if fld == "" {
			delete(c.Repos, name)
			return nil
		}
		f, ok := repoFields[fld]
		if !ok {
			return unknownKey(key)
		}
		f.unset(&r)
		c.Repos[name] = r
		return nil
	case "providers":
		p := findProvider(c, name)
		if p == nil {
			return fmt.Errorf("no provider %q", name)
		}
		if fld == "" {
			var kept []cfg.Provider
			for _, pr := range c.Providers {
				if pr.Name != name {
					kept = append(kept, pr)
				}
			}
			c.Providers = kept
			return nil
		}
		f, ok := providerFields[fld]
		if !ok {
			return unknownKey(key)
		}
		f.unset(p)
		return nil
	}
	return unknownKey(key)
}

// checkRoute rejects an invalid `integration` value. The empty string means
// "inherit" in the struct, but it is not something `set` accepts - clearing a
// key is what `unset` is for.
func checkRoute(v string) error {
	if v == "" || !cfg.ValidRoute(v) {
		return fmt.Errorf("integration must be %s", strings.Join(cfg.IntegrationRoutes, " or "))
	}
	return nil
}

// findProvider returns a pointer to the named provider in c, or nil.
func findProvider(c *cfg.Config, name string) *cfg.Provider {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// patternString renders a pattern as a config-CLI entry.
func patternString(p cfg.Pattern) string {
	key := p.Prefix
	if p.Regex != "" {
		key = "regex:" + p.Regex
	}
	if p.Repo != "" {
		return key + "=" + p.Repo
	}
	return key
}

// parsePatternEntry parses a `prefix` or `prefix=repo` entry (regex entries are
// edited via `config edit`, not this shorthand).
func parsePatternEntry(v string) cfg.Pattern {
	if i := strings.Index(v, "="); i >= 0 {
		return cfg.Pattern{Prefix: v[:i], Repo: v[i+1:]}
	}
	return cfg.Pattern{Prefix: v}
}
