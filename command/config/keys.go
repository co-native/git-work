package config

import (
	"fmt"
	"strconv"
	"strings"

	cfg "github.com/co-native/git-work/internal/config"
	"gopkg.in/yaml.v3"
)

// getKey returns the printable value for a dot-path key.
func getKey(c *cfg.Config, key string) (string, error) {
	seg := strings.Split(key, ".")
	switch seg[0] {
	case "paths":
		if len(seg) != 2 {
			return "", fmt.Errorf("unknown key %q", key)
		}
		switch seg[1] {
		case "repos":
			return c.Paths.Repos, nil
		case "work":
			return c.Paths.Work, nil
		}
		return "", fmt.Errorf("unknown key %q", key)
	case "defaults":
		if len(seg) != 2 {
			return "", fmt.Errorf("unknown key %q", key)
		}
		switch seg[1] {
		case "integration":
			return c.Defaults.Integration, nil
		}
		return "", fmt.Errorf("unknown key %q", key)
	case "repos":
		if len(seg) < 2 {
			return "", fmt.Errorf("unknown key %q", key)
		}
		r, ok := c.Repos[seg[1]]
		if !ok {
			return "", fmt.Errorf("no repo config for %q", seg[1])
		}
		if len(seg) == 2 {
			// The whole entry as YAML, mirroring `providers.<name>`.
			out, err := yaml.Marshal(r)
			return string(out), err
		}
		switch seg[2] {
		case "integration":
			return r.Integration, nil
		case "add_by_default":
			return strconv.FormatBool(r.AddByDefault), nil
		}
		return "", fmt.Errorf("unknown key %q", key)
	case "providers":
		if len(seg) < 2 {
			return "", fmt.Errorf("unknown key %q", key)
		}
		p := findProvider(c, seg[1])
		if p == nil {
			return "", fmt.Errorf("no provider %q", seg[1])
		}
		if len(seg) == 2 {
			// Marshal the whole provider as YAML and return it.
			out, err := yaml.Marshal(p)
			return string(out), err
		}
		switch seg[2] {
		case "type":
			return p.Type, nil
		case "default":
			return strconv.FormatBool(p.Default), nil
		case "folder_case":
			return p.FolderCase, nil
		case "branch_case":
			return p.BranchCase, nil
		case "patterns":
			var lines []string
			for _, pat := range p.Patterns {
				lines = append(lines, patternString(pat))
			}
			return strings.Join(lines, "\n"), nil
		}
		return "", fmt.Errorf("unknown key %q", key)
	}
	return "", fmt.Errorf("unknown key %q", key)
}

// setKey applies values to a dot-path key.
func setKey(c *cfg.Config, key string, values []string) error {
	seg := strings.Split(key, ".")
	switch seg[0] {
	case "paths":
		if len(seg) != 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		if len(values) != 1 {
			return fmt.Errorf("key %q takes exactly one value", key)
		}
		switch seg[1] {
		case "repos":
			c.Paths.Repos = values[0]
		case "work":
			c.Paths.Work = values[0]
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		return nil
	case "defaults":
		if len(seg) != 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		if len(values) != 1 {
			return fmt.Errorf("key %q takes exactly one value", key)
		}
		switch seg[1] {
		case "integration":
			if err := checkRoute(values[0]); err != nil {
				return err
			}
			c.Defaults.Integration = values[0]
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		return nil
	case "repos":
		if len(seg) != 3 {
			return fmt.Errorf("unknown key %q", key)
		}
		if len(values) != 1 {
			return fmt.Errorf("key %q takes exactly one value", key)
		}
		// Load leaves a nil map for a config with no `repos:` block, and
		// assigning into a nil map panics rather than erroring.
		if c.Repos == nil {
			c.Repos = map[string]cfg.RepoConfig{}
		}
		// Indexing a missing key yields the zero RepoConfig, so this is the
		// auto-create path too - no append-then-take-a-pointer dance.
		r := c.Repos[seg[1]]
		switch seg[2] {
		case "integration":
			if err := checkRoute(values[0]); err != nil {
				return err
			}
			r.Integration = values[0]
		case "add_by_default":
			b, err := strconv.ParseBool(values[0])
			if err != nil {
				return fmt.Errorf("add_by_default must be true or false: %w", err)
			}
			r.AddByDefault = b
		default:
			return fmt.Errorf("cannot set %q", key)
		}
		// Map elements are not addressable, so the mutation is written back.
		c.Repos[seg[1]] = r
		return nil
	case "providers":
		if len(seg) != 3 {
			return fmt.Errorf("unknown key %q", key)
		}
		// Auto-create provider if missing.
		p := findProvider(c, seg[1])
		if p == nil {
			c.Providers = append(c.Providers, cfg.Provider{Name: seg[1]})
			p = &c.Providers[len(c.Providers)-1]
		}
		switch seg[2] {
		case "type":
			if len(values) != 1 {
				return fmt.Errorf("key %q takes exactly one value", key)
			}
			p.Type = values[0]
		case "default":
			if len(values) != 1 {
				return fmt.Errorf("key %q takes exactly one value", key)
			}
			b, err := strconv.ParseBool(values[0])
			if err != nil {
				return fmt.Errorf("default must be true or false: %w", err)
			}
			if b {
				for i := range c.Providers {
					c.Providers[i].Default = false
				}
			}
			p.Default = b
		case "folder_case":
			if len(values) != 1 {
				return fmt.Errorf("key %q takes exactly one value", key)
			}
			p.FolderCase = values[0]
		case "branch_case":
			if len(values) != 1 {
				return fmt.Errorf("key %q takes exactly one value", key)
			}
			p.BranchCase = values[0]
		case "patterns":
			var pats []cfg.Pattern
			for _, v := range values {
				pats = append(pats, parsePatternEntry(v))
			}
			p.Patterns = pats
		default:
			return fmt.Errorf("cannot set %q", key)
		}
		return nil
	}
	return fmt.Errorf("unknown key %q", key)
}

// unsetKey clears a scalar (resetting required paths to default).
func unsetKey(c *cfg.Config, key string) error {
	seg := strings.Split(key, ".")
	switch seg[0] {
	case "paths":
		if len(seg) != 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		d := cfg.Default()
		switch seg[1] {
		case "repos":
			c.Paths.Repos = d.Paths.Repos
		case "work":
			c.Paths.Work = d.Paths.Work
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		return nil
	case "defaults":
		if len(seg) != 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		switch seg[1] {
		case "integration":
			c.Defaults.Integration = ""
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		return nil
	case "repos":
		if len(seg) < 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		r, ok := c.Repos[seg[1]]
		if !ok {
			return fmt.Errorf("no repo config for %q", seg[1])
		}
		if len(seg) == 2 {
			// Drop the entry entirely, mirroring `unset providers.<name>`.
			delete(c.Repos, seg[1])
			return nil
		}
		switch seg[2] {
		case "integration":
			r.Integration = ""
		case "add_by_default":
			r.AddByDefault = false
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		c.Repos[seg[1]] = r
		return nil
	case "providers":
		if len(seg) < 2 {
			return fmt.Errorf("unknown key %q", key)
		}
		p := findProvider(c, seg[1])
		if p == nil {
			return fmt.Errorf("no provider %q", seg[1])
		}
		if len(seg) == 2 {
			// Drop the provider entirely.
			var filtered []cfg.Provider
			for _, pr := range c.Providers {
				if pr.Name != seg[1] {
					filtered = append(filtered, pr)
				}
			}
			c.Providers = filtered
			return nil
		}
		switch seg[2] {
		case "type":
			p.Type = ""
		case "default":
			p.Default = false
		case "folder_case":
			p.FolderCase = ""
		case "branch_case":
			p.BranchCase = ""
		case "patterns":
			p.Patterns = nil
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		return nil
	}
	return fmt.Errorf("unknown key %q", key)
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
