package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/config"
)

func providers() []config.Provider {
	return []config.Provider{
		{Name: "github", Type: "github", Patterns: []config.Pattern{
			{Prefix: "dev-tools", Repo: "acme/api"},
		}},
		{Name: "jira", Type: "jira", Default: true, Patterns: []config.Pattern{{Prefix: "PROJ"}}},
	}
}

func TestResolveGitHubCaptures(t *testing.T) {
	m, err := Resolve(providers(), "DEV-TOOLS-7") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if m.Provider == nil || m.Provider.Name != "github" || m.Repo != "acme/api" || m.Number != "7" {
		t.Fatalf("match = %#v", m)
	}
}

func TestResolveJiraPattern(t *testing.T) {
	m, _ := Resolve(providers(), "proj-10")
	if m.Provider == nil || m.Provider.Name != "jira" || m.Repo != "" || m.Number != "" {
		t.Fatalf("match = %#v", m)
	}
}

func TestResolveDefaultFallback(t *testing.T) {
	m, _ := Resolve(providers(), "OPS-3") // no pattern matches -> jira default
	if m.Provider == nil || m.Provider.Name != "jira" {
		t.Fatalf("match = %#v", m)
	}
}

func TestResolveExplicitBeatsDefault(t *testing.T) {
	provs := []config.Provider{
		{Name: "gh", Type: "github", Patterns: []config.Pattern{{Prefix: "proj", Repo: "o/r"}}},
		{Name: "jira", Type: "jira", Default: true},
	}
	m, _ := Resolve(provs, "PROJ-10")
	if m.Provider == nil || m.Provider.Name != "gh" || m.Number != "10" {
		t.Fatalf("match = %#v", m)
	}
}

func TestResolveNoMatchNoDefault(t *testing.T) {
	provs := []config.Provider{{Name: "gh", Type: "github", Patterns: []config.Pattern{{Prefix: "x", Repo: "o/r"}}}}
	m, _ := Resolve(provs, "WEIRD-1")
	if m.Provider != nil {
		t.Fatalf("expected no provider, got %#v", m)
	}
}

func TestResolveRegexCaptures(t *testing.T) {
	provs := []config.Provider{{Name: "gh", Type: "github", Patterns: []config.Pattern{{Regex: `^gh-(\d+)$`, Repo: "o/r"}}}}
	m, _ := Resolve(provs, "GH-42")
	if m.Provider == nil || m.Number != "42" {
		t.Fatalf("match = %#v", m)
	}
}

func TestParseDetailsJira(t *testing.T) {
	raw := []byte(`{"fields":{"summary":"Fix login","description":"plain"}}`)
	d, err := parseDetails(raw, ".fields.summary", ".fields.description")
	if err != nil || d.Title != "Fix login" || d.Description != "plain" {
		t.Fatalf("d=%#v err=%v", d, err)
	}
}

func TestParseDetailsGitHub(t *testing.T) {
	raw := []byte(`{"title":"Add docs","body":"- one\n- two"}`)
	d, err := parseDetails(raw, ".title", ".body")
	if err != nil || d.Title != "Add docs" || d.Description != "- one\n- two" {
		t.Fatalf("d=%#v err=%v", d, err)
	}
}

func TestExtractDotPath(t *testing.T) {
	doc := map[string]any{
		"fields": map[string]any{"summary": "Fix login", "description": "Details here"},
	}
	if got := extract(doc, ".fields.summary"); got != "Fix login" {
		t.Errorf("extract = %q", got)
	}
	if got := extract(doc, ".missing.path"); got != "" {
		t.Errorf("missing path should be empty, got %q", got)
	}
}

// unmarshal parses a JSON string the way Fetch does (into `any`, so nodes are
// map[string]any and arrays are []any).
func unmarshal(t *testing.T, raw string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

func TestExtractRendersADFDescription(t *testing.T) {
	// Mirrors the real Jira Cloud shape: a doc with an intro paragraph, a
	// bullet list, and a closing paragraph.
	doc := unmarshal(t, `{"fields":{"description":{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"This task includes:"}]},
		{"type":"bulletList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"User docs"}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Dev docs"}]}]}
		]},
		{"type":"paragraph","content":[{"type":"text","text":"Done."}]}
	]}}}`)
	want := "This task includes:\n\n- User docs\n- Dev docs\n\nDone."
	if got := extract(doc, ".fields.description"); got != want {
		t.Errorf("extract =\n%q\nwant\n%q", got, want)
	}
}

func TestADFOrderedListAndHardBreak(t *testing.T) {
	doc := unmarshal(t, `{"type":"doc","content":[
		{"type":"heading","content":[{"type":"text","text":"Steps"}]},
		{"type":"orderedList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"First"}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Second"}]}]}
		]},
		{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"hardBreak"},{"type":"text","text":"b"}]}
	]}`)
	want := "Steps\n\n1. First\n2. Second\n\na\nb"
	if got := strings.TrimSpace(adfToText(doc)); got != want {
		t.Errorf("adfToText =\n%q\nwant\n%q", got, want)
	}
}

// A non-doc, non-string leaf (e.g. a number or array) still yields "".
func TestExtractNonStringNonDocLeaf(t *testing.T) {
	doc := unmarshal(t, `{"fields":{"votes":3,"labels":["a","b"]}}`)
	if got := extract(doc, ".fields.votes"); got != "" {
		t.Errorf("number leaf = %q, want empty", got)
	}
	if got := extract(doc, ".fields.labels"); got != "" {
		t.Errorf("array leaf = %q, want empty", got)
	}
}
