package template

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	beginMarker = "<!-- git-work:begin -->"
	endMarker   = "<!-- git-work:end -->"
)

// MergeManagedBlock replaces the content between the markers in existing with
// managed. If markers are absent, a managed block is prepended and the existing
// content preserved below it.
func MergeManagedBlock(existing, managed string) string {
	block := beginMarker + "\n" + strings.TrimRight(managed, "\n") + "\n" + endMarker
	b := strings.Index(existing, beginMarker)
	e := strings.Index(existing, endMarker)
	if b >= 0 && e > b {
		before := existing[:b]
		after := existing[e+len(endMarker):]
		return before + block + after
	}
	if strings.TrimSpace(existing) == "" {
		return block + "\n"
	}
	return block + "\n\n" + existing
}

// WriteManaged merges managed content into the file at path (creating it if needed).
func WriteManaged(path, managed string) error {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(MergeManagedBlock(existing, managed)), 0o644)
}

// RepoRef names one repo included in a work folder and the branch checked
// out in its worktree, which may differ from the folder-level work branch
// (a reused or adopted existing branch).
type RepoRef struct {
	Name   string
	Branch string
}

// workFolderDoc is git-work's own description of the work folder contract,
// inlined into every generated CLAUDE.md. It ships in the binary rather than
// being installed alongside it so that there is nothing to keep in sync per
// machine, and so the text always matches the version of the tool that wrote
// it. `git work refresh` propagates an upgrade.
//
//go:embed workfolder.md
var workFolderDoc string

// ClaudeInput is everything AggregateClaude needs. It is a struct rather than a
// parameter list because the two overlay sources pushed it past the point where
// positional arguments stayed readable at the four call sites.
type ClaudeInput struct {
	// ReposRoot is where per-repo overlays are looked up
	// (<ReposRoot>/<repo>/CLAUDE.md).
	ReposRoot string
	// OverlayPath is the optional operator-level overlay inlined for every
	// work folder, normally config.OverlayPath(). Empty or missing is fine.
	OverlayPath string
	// TicketID is empty for a ticketless work folder, which renders the
	// title alone.
	TicketID string
	Title    string
	// Branch is the folder-level work branch, used for any repo that did not
	// record its own.
	Branch string
	Repos  []RepoRef
}

// AggregateClaude builds the managed block for a work folder's CLAUDE.md.
//
// Section order is fixed: the folder's identity header, then git-work's own work
// folder contract, then the operator's overlay, then one section per repo with
// that repo's overlay inlined. General rules precede repo specifics, and the
// header stays first so the block still opens by naming the folder.
func AggregateClaude(in ClaudeInput) string {
	var sb strings.Builder
	if in.TicketID == "" {
		fmt.Fprintf(&sb, "# %s\nBranch: %s\n", in.Title, in.Branch)
	} else {
		fmt.Fprintf(&sb, "# %s: %s\nBranch: %s\n", in.TicketID, in.Title, in.Branch)
	}

	appendFile(&sb, workFolderDoc)
	if in.OverlayPath != "" {
		if data, err := os.ReadFile(in.OverlayPath); err == nil {
			appendFile(&sb, string(data))
		}
	}

	for _, r := range in.Repos {
		name := r.Name
		b := r.Branch
		if b == "" {
			b = in.Branch
		}
		fmt.Fprintf(&sb, "\n## %s (./%s)\nBranch: %s\n", name, name, b)
		overlay := filepath.Join(in.ReposRoot, name, "CLAUDE.md")
		if data, err := os.ReadFile(overlay); err == nil {
			appendFile(&sb, string(data))
		}
	}
	return sb.String()
}

// appendFile writes inlined content after a blank line, collapsing whatever
// trailing newlines the source happens to have down to exactly one so sections
// abut consistently. An assembled overlay typically arrives with two, a
// hand-written one with none, and neither should change the spacing of the
// generated block.
//
// Content is otherwise inlined verbatim: an installer's leading HTML comment is
// left in place, since Claude Code strips block-level comments before reading a
// CLAUDE.md and rewriting them here would just be another thing to keep true.
func appendFile(sb *strings.Builder, content string) {
	content = strings.TrimRight(content, "\n")
	if strings.TrimSpace(content) == "" {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(content)
	sb.WriteString("\n")
}

// Ticket builds the managed block for TICKET.md.
func Ticket(ticketID, title, description string) string {
	return fmt.Sprintf("# %s: %s\n\n%s\n", ticketID, title, strings.TrimSpace(description))
}

// Readme builds the managed block for README.md.
func Readme(ticketID, branch string, repos []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\nBranch: %s\n\n## Repos\n", ticketID, branch)
	for _, r := range repos {
		fmt.Fprintf(&sb, "- %s (./%s)\n", r, r)
	}
	return sb.String()
}
