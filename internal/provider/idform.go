package provider

import (
	"regexp"
	"strings"
)

// DisplayID returns the uppercase form of a ticket id, used for display and as
// the Jira lookup id.
func DisplayID(id string) string { return strings.ToUpper(id) }

// CaseID recases a ticket id for folder/branch names. "lower" lowercases;
// anything else (including "" and "upper") uppercases.
func CaseID(id, mode string) string {
	if mode == "lower" {
		return strings.ToLower(id)
	}
	return strings.ToUpper(id)
}

// prefixRegex expands a prefix into a case-insensitive matcher whose single
// capture group is the trailing issue number.
func prefixRegex(prefix string) string {
	return "(?i)^" + regexp.QuoteMeta(prefix) + "-([0-9]+)$"
}
