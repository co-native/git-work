package provider

import (
	"regexp"
	"testing"
)

func TestDisplayID(t *testing.T) {
	if got := DisplayID("dev-tools-1"); got != "DEV-TOOLS-1" {
		t.Errorf("DisplayID = %q", got)
	}
}

func TestCaseID(t *testing.T) {
	cases := []struct{ id, mode, want string }{
		{"dev-tools-1", "", "DEV-TOOLS-1"},
		{"dev-tools-1", "upper", "DEV-TOOLS-1"},
		{"PROJ-10", "lower", "proj-10"},
	}
	for _, c := range cases {
		if got := CaseID(c.id, c.mode); got != c.want {
			t.Errorf("CaseID(%q,%q) = %q, want %q", c.id, c.mode, got, c.want)
		}
	}
}

func TestPrefixRegexMatchesAndCaptures(t *testing.T) {
	re := regexp.MustCompile(prefixRegex("dev-tools"))
	sub := re.FindStringSubmatch("DEV-TOOLS-12") // case-insensitive
	if sub == nil || sub[1] != "12" {
		t.Fatalf("submatch = %#v", sub)
	}
	if re.MatchString("dev-tools-12-extra") {
		t.Error("should not match trailing junk")
	}
}

func TestPrefixRegexQuotesMeta(t *testing.T) {
	// A prefix containing regex metachars must be matched literally.
	re := regexp.MustCompile(prefixRegex("a.b"))
	if !re.MatchString("A.B-3") {
		t.Error("literal dot should match")
	}
	if re.MatchString("axb-3") {
		t.Error("dot must not act as wildcard")
	}
}
