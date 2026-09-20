package template

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/state"
)

// The files git-work generates at a work folder's root. The instructions file
// is named by config (config.InstructionsAgents or config.InstructionsClaude).
const (
	TicketFile = "TICKET.md"
	ReadmeFile = "README.md"
)

// GeneratedFiles lists every file git-work generates at a work folder's root:
// the set `done` may delete without --remove-all. Both instructions file names
// are in the set, because a folder written under one `instructions_file`
// setting keeps that name until the next re-render migrates it.
var GeneratedFiles = []string{TicketFile, ReadmeFile, config.InstructionsAgents, config.InstructionsClaude}

// TicketDetails is what WriteWorkFolder renders TICKET.md from. Title, when
// set, also replaces st.Title in the other generated files, so a freshly
// fetched title shows everywhere at once.
type TicketDetails struct {
	Title       string
	Description string
}

// WriteWorkFolder renders the work folder's generated files from st: the
// README.md and instructions file managed blocks always, and TICKET.md when
// ticket is given and the folder has a ticket (a ticketless folder never gets
// one).
//
// reposRoot is the resolved repos root (layout.Layout's), not the raw config
// value: it locates the per-repo overlays on disk, so an unexpanded ~ would
// find none and silently drop them from the generated file.
//
// file is the instructions file name (Config.Instructions). An instructions
// file the folder holds under the other name is migrated first, see
// MigrateInstructions.
func WriteWorkFolder(workDir, reposRoot, file string, st *state.State, ticket *TicketDetails) error {
	title := st.Title
	if ticket != nil && ticket.Title != "" {
		title = ticket.Title
	}
	if ticket != nil && !st.NoTicket {
		if err := WriteManaged(filepath.Join(workDir, TicketFile),
			Ticket(st.TicketID, title, ticket.Description)); err != nil {
			return err
		}
	}
	repos := make([]string, len(st.Repos))
	refs := make([]RepoRef, len(st.Repos))
	for i, r := range st.Repos {
		repos[i] = r.Name
		refs[i] = RepoRef{Name: r.Name, Branch: r.Branch}
	}
	if err := WriteManaged(filepath.Join(workDir, ReadmeFile),
		Readme(st.DisplayName(), st.Branch, repos)); err != nil {
		return err
	}
	if err := MigrateInstructions(workDir, file); err != nil {
		return err
	}
	return WriteManaged(filepath.Join(workDir, file),
		AggregateInstructions(InstructionsInput{
			File:       file,
			ReposRoot:  reposRoot,
			OverlayDir: config.OverlayDir(),
			TicketID:   st.TicketID,
			Title:      title,
			Branch:     st.Branch,
			Repos:      refs,
		}))
}

// MigrateInstructions moves a generated instructions file that workDir holds
// under the other name to file, so the folder never carries both: Claude Code
// reads only CLAUDE.md when both exist, and the stale one would win. A file
// without the markers is not git-work's and is left alone. When only the old
// file exists it is renamed, so content outside the markers moves with it.
// When both exist, the managed block is cut out of the old file (the file is
// deleted if nothing else is in it) and the caller's re-render puts a fresh
// block into the new one, ahead of whatever the new file already held.
func MigrateInstructions(workDir, file string) error {
	from := filepath.Join(workDir, config.OtherInstructionsFile(file))
	to := filepath.Join(workDir, file)
	data, err := os.ReadFile(from)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	before, after, ok := splitManaged(string(data))
	if !ok {
		return nil
	}
	if _, err := os.Stat(to); os.IsNotExist(err) {
		return os.Rename(from, to)
	} else if err != nil {
		return err
	}
	rest := strings.TrimSpace(before + after)
	if rest == "" {
		return os.Remove(from)
	}
	return os.WriteFile(from, []byte(rest+"\n"), 0o644)
}

const (
	beginMarker = "<!-- git-work:begin -->"
	endMarker   = "<!-- git-work:end -->"
)

// splitManaged returns the content before and after the managed block in s,
// and false when s has no managed block.
func splitManaged(s string) (before, after string, ok bool) {
	b := strings.Index(s, beginMarker)
	e := strings.Index(s, endMarker)
	if b < 0 || e <= b {
		return "", "", false
	}
	return s[:b], s[e+len(endMarker):], true
}

// MergeManagedBlock replaces the content between the markers in existing with
// managed. If markers are absent, a managed block is prepended and the existing
// content preserved below it.
func MergeManagedBlock(existing, managed string) string {
	block := beginMarker + "\n" + strings.TrimRight(managed, "\n") + "\n" + endMarker
	if before, after, ok := splitManaged(existing); ok {
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
// inlined into every generated instructions file. It ships in the binary rather than
// being installed alongside it so that there is nothing to keep in sync per
// machine, and so the text always matches the version of the tool that wrote
// it. `git work refresh` propagates an upgrade.
//
//go:embed workfolder.md
var workFolderDoc string

// InstructionsInput is everything AggregateInstructions needs. It is a struct
// rather than a parameter list because the two overlay sources pushed it past
// the point where positional arguments stayed readable.
type InstructionsInput struct {
	// File is the instructions file name being generated. It also sets the
	// overlay lookup order: an overlay named like File is preferred, and one
	// under the other name is the fallback, so a switch of the setting does
	// not require the overlays to be renamed.
	File string
	// ReposRoot is where per-repo overlays are looked up
	// (<ReposRoot>/<repo>/<File>).
	ReposRoot string
	// OverlayDir is the directory holding the optional operator-level overlay
	// inlined for every work folder, normally config.OverlayDir(). Empty, or
	// a directory with no overlay, is fine.
	OverlayDir string
	// TicketID is empty for a ticketless work folder, which renders the
	// title alone.
	TicketID string
	Title    string
	// Branch is the folder-level work branch, used for any repo that did not
	// record its own.
	Branch string
	Repos  []RepoRef
}

// AggregateInstructions builds the managed block for a work folder's
// instructions file.
//
// Section order is fixed: the folder's identity header, then git-work's own work
// folder contract, then the operator's overlay, then one section per repo with
// that repo's overlay inlined. General rules precede repo specifics, and the
// header stays first so the block still opens by naming the folder.
func AggregateInstructions(in InstructionsInput) string {
	file := in.File
	if file == "" {
		file = config.InstructionsAgents
	}
	var sb strings.Builder
	if in.TicketID == "" {
		fmt.Fprintf(&sb, "# %s\nBranch: %s\n", in.Title, in.Branch)
	} else {
		fmt.Fprintf(&sb, "# %s: %s\nBranch: %s\n", in.TicketID, in.Title, in.Branch)
	}

	appendFile(&sb, workFolderDoc)
	if in.OverlayDir != "" {
		appendOverlay(&sb, in.OverlayDir, file)
	}

	for _, r := range in.Repos {
		name := r.Name
		b := r.Branch
		if b == "" {
			b = in.Branch
		}
		fmt.Fprintf(&sb, "\n## %s (./%s)\nBranch: %s\n", name, name, b)
		appendOverlay(&sb, filepath.Join(in.ReposRoot, name), file)
	}
	return sb.String()
}

// appendOverlay inlines the overlay found in dir: <dir>/<file> if it exists,
// else the file under the other instructions name, else nothing.
func appendOverlay(sb *strings.Builder, dir, file string) {
	for _, name := range []string{file, config.OtherInstructionsFile(file)} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			appendFile(sb, string(data))
			return
		}
	}
}

// appendFile writes inlined content after a blank line, collapsing whatever
// trailing newlines the source happens to have down to exactly one so sections
// abut consistently. An assembled overlay typically arrives with two, a
// hand-written one with none, and neither should change the spacing of the
// generated block.
//
// A leading HTML comment (an installer's "generated from" marker) is dropped.
// Claude Code strips block-level comments from CLAUDE.md before reading it,
// but does not document the same for AGENTS.md, and other agents make no such
// promise. The rest of the content is inlined verbatim.
func appendFile(sb *strings.Builder, content string) {
	content = stripLeadingComment(content)
	content = strings.TrimRight(content, "\n")
	if strings.TrimSpace(content) == "" {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(content)
	sb.WriteString("\n")
}

// stripLeadingComment removes an HTML comment that opens content, and the
// blank lines after it. Content that does not open with a comment is returned
// unchanged, as is content whose opening comment never closes.
func stripLeadingComment(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<!--") {
		return content
	}
	end := strings.Index(trimmed, "-->")
	if end < 0 {
		return content
	}
	return strings.TrimLeft(trimmed[end+len("-->"):], " \t\r\n")
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
