package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/co-native/git-work/internal/config"
)

// Details holds fetched ticket info.
type Details struct {
	Title       string
	Description string
}

// Match is the result of resolving a ticket id against the providers.
type Match struct {
	Provider *config.Provider // nil if nothing matched
	Repo     string           // github: repo of the matched pattern
	Number   string           // github: captured issue number (group 1)
}

// Resolve picks the provider+pattern for a ticket id. The first matching
// pattern wins; otherwise the provider flagged Default is used. Matching is
// case-insensitive. Returns a zero Match (nil Provider) if nothing matches.
func Resolve(provs []config.Provider, id string) (Match, error) {
	var def *config.Provider
	for i := range provs {
		p := &provs[i]
		if p.Default {
			def = p
		}
		for _, pat := range p.Patterns {
			expr := pat.Regex
			if pat.Prefix != "" {
				expr = prefixRegex(pat.Prefix)
			} else {
				expr = "(?i)" + expr
			}
			re, err := regexp.Compile(expr)
			if err != nil {
				return Match{}, fmt.Errorf("provider %s: invalid pattern: %w", p.Name, err)
			}
			sub := re.FindStringSubmatch(id)
			if sub == nil {
				continue
			}
			m := Match{Provider: p}
			if p.Type == "github" {
				m.Repo = pat.Repo
				if len(sub) > 1 {
					m.Number = sub[1]
				}
			}
			return m, nil
		}
	}
	if def != nil {
		return Match{Provider: def}, nil
	}
	return Match{}, nil
}

// run executes a command and returns stdout, or an error including stderr. It
// is a package var so tests can stub it.
var run = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Fetch retrieves ticket details for a resolved match, dispatching on provider
// type. The id is the raw typed id; jira uses its uppercase display form.
func Fetch(m Match, id string) (*Details, error) {
	switch m.Provider.Type {
	case "jira":
		out, err := run("jira", "issue", "view", DisplayID(id), "--raw")
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", m.Provider.Name, err)
		}
		return parseDetails(out, ".fields.summary", ".fields.description")
	case "github":
		out, err := run("gh", "issue", "view", m.Number, "--repo", m.Repo, "--json", "title,body")
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", m.Provider.Name, err)
		}
		return parseDetails(out, ".title", ".body")
	default:
		return nil, fmt.Errorf("provider %s: unsupported type %q", m.Provider.Name, m.Provider.Type)
	}
}

// parseDetails unmarshals command JSON and extracts the title/description
// dot-paths (description may be Jira ADF, which extract renders to text).
func parseDetails(out []byte, titlePath, descPath string) (*Details, error) {
	var doc any
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("output not JSON: %w", err)
	}
	return &Details{
		Title:       extract(doc, titlePath),
		Description: extract(doc, descPath),
	}, nil
}

// extract resolves a dot-path (e.g. ".fields.summary") into a string. Missing
// paths return "". A string leaf is returned as-is; an Atlassian Document
// Format object (Jira Cloud rich text, type "doc") is rendered to plain text.
// Any other leaf type returns "".
func extract(doc any, path string) string {
	if path == "" {
		return ""
	}
	cur := doc
	for _, key := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[key]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case map[string]any:
		if v["type"] == "doc" {
			return strings.TrimSpace(adfToText(v))
		}
	}
	return ""
}

// adfToText renders an Atlassian Document Format node tree (Jira Cloud rich
// text) to plain text. Block-level siblings are separated by a blank line,
// list items are kept tight, and inline runs are concatenated. Unrecognized
// node types fall back to rendering their content, so unsupported nodes
// degrade to their text rather than disappearing.
func adfToText(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	typ, _ := m["type"].(string)
	content, _ := m["content"].([]any)
	switch typ {
	case "text":
		s, _ := m["text"].(string)
		return s
	case "hardBreak":
		return "\n"
	case "emoji", "mention":
		if attrs, ok := m["attrs"].(map[string]any); ok {
			if t, _ := attrs["text"].(string); t != "" {
				return t
			}
		}
		return ""
	case "paragraph", "heading", "codeBlock":
		return inlineText(content)
	case "bulletList", "orderedList":
		return listText(typ, content)
	case "listItem":
		return blocksText(content, "\n")
	default: // doc, blockquote, panel, and unknown containers
		return blocksText(content, "\n\n")
	}
}

// inlineText concatenates the rendered inline children (text, hardBreak, …).
func inlineText(nodes []any) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(adfToText(n))
	}
	return sb.String()
}

// blocksText renders block-level siblings, dropping empties and joining the
// rest with sep.
func blocksText(nodes []any, sep string) string {
	var parts []string
	for _, n := range nodes {
		if s := strings.Trim(adfToText(n), "\n"); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}

// listText renders a bullet/ordered list, prefixing each item with its marker
// and indenting any continuation lines under it.
func listText(typ string, items []any) string {
	var parts []string
	for i, it := range items {
		marker := "- "
		if typ == "orderedList" {
			marker = strconv.Itoa(i+1) + ". "
		}
		lines := strings.Split(adfToText(it), "\n")
		for j := range lines {
			if j == 0 {
				lines[j] = marker + lines[j]
			} else {
				lines[j] = strings.Repeat(" ", len(marker)) + lines[j]
			}
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n")
}
