package provider

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/co-native/git-work/internal/config"
)

// Validate checks provider definitions against the schema rules. It is called
// by commands that resolve providers (new, refresh) before use.
func Validate(provs []config.Provider) error {
	for _, p := range provs {
		if p.Type != "jira" && p.Type != "github" {
			return fmt.Errorf("provider %q: invalid type %q (want jira or github)", p.Name, p.Type)
		}
		if p.Type == "github" && p.Default {
			return fmt.Errorf("provider %q: type github cannot be default", p.Name)
		}
		for _, c := range []string{p.FolderCase, p.BranchCase} {
			if c != "" && c != "upper" && c != "lower" {
				return fmt.Errorf("provider %q: case must be upper or lower, got %q", p.Name, c)
			}
		}
		for _, pat := range p.Patterns {
			if (pat.Prefix == "") == (pat.Regex == "") {
				return fmt.Errorf("provider %q: each pattern needs exactly one of prefix/regex", p.Name)
			}
			if pat.Regex != "" {
				re, err := regexp.Compile("(?i)" + pat.Regex)
				if err != nil {
					return fmt.Errorf("provider %q: invalid regex %q: %w", p.Name, pat.Regex, err)
				}
				if p.Type == "github" && re.NumSubexp() < 1 {
					return fmt.Errorf("provider %q: github regex %q needs a capture group for the issue number", p.Name, pat.Regex)
				}
			}
			switch p.Type {
			case "github":
				if !strings.Contains(pat.Repo, "/") {
					return fmt.Errorf("provider %q: github pattern needs an owner/name repo", p.Name)
				}
			case "jira":
				if pat.Repo != "" {
					return fmt.Errorf("provider %q: jira pattern must not set repo", p.Name)
				}
			}
		}
	}
	return nil
}
