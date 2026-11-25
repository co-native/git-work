// Package naming derives the names a work folder carries: its directory name,
// its slug, and its work branch. `new` derives them when it creates a folder,
// `adopt` infers them for an unmanaged one, and `rename` derives them again
// before moving the folder, its state and its branches together. One home keeps
// those three from disagreeing about what a folder is called.
package naming

import (
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Slug reduces a name to the folder/branch form: lowercased, runs of
// non-alphanumeric characters collapsed to single dashes, with no leading or
// trailing dash.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ticketRe matches a leading ticket id: one or more "<word>-" segments
// followed by an issue number, so multi-segment prefixes like "dev-tools-1"
// work too.
var ticketRe = regexp.MustCompile(`^((?:[A-Za-z][A-Za-z0-9]*-)+[0-9]+)(?:-(.+))?$`)

// InferTicket splits a work-folder name into ticket id and slug: a leading
// "<prefix>-<number>" is the ticket ("PROJ-3-git-work" -> "PROJ-3" +
// "git-work", "dev-tools-12-fix" -> "dev-tools-12" + "fix"); a name without
// one is not a ticket at all (empty ticket and slug - the ticketless case).
func InferTicket(folder string) (ticket, slug string) {
	if m := ticketRe.FindStringSubmatch(folder); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// SlugForFolder returns the slug a folder name implies for a folder belonging
// to ticketID, or for a ticketless folder when ticketID is empty: the name
// without the leading "<ticket>-" segment, in slug form. A ticketless folder's
// name is its slug, so the result is the slugified name.
func SlugForFolder(folder, ticketID string) string {
	folder = strings.TrimSpace(folder)
	if ticketID != "" {
		n := len(ticketID)
		if len(folder) > n+1 && folder[n] == '-' && strings.EqualFold(folder[:n], ticketID) {
			return Slug(folder[n+1:])
		}
	}
	return Slug(folder)
}

// BranchForRename returns the work branch that follows a folder rename: the
// old branch with the segment naming oldSlug replaced by newSlug. That is how
// `new` derives a default branch (<ticket>-<slug>, or the slug alone for a
// ticketless folder), so the common rename keeps the branch in step without
// consulting a provider's case rule. ok is false when oldBranch does not end
// with oldSlug, which means the branch was named by hand: the caller asks for
// --branch or --keep-branch instead of guessing.
func BranchForRename(oldBranch, oldSlug, newSlug string) (branch string, ok bool) {
	if oldSlug == "" || newSlug == "" {
		return "", false
	}
	if oldBranch == oldSlug {
		return newSlug, true
	}
	if prefix, found := strings.CutSuffix(oldBranch, "-"+oldSlug); found {
		return prefix + "-" + newSlug, true
	}
	return "", false
}

// FolderNameProblem reports why name cannot be a work folder directory name, or
// "" when it can. A work folder name is a single path segment: it must not
// escape the work root, and it must not be git-work's reserved `.worktrees`.
func FolderNameProblem(name string) string {
	switch {
	case name == "":
		return "the name is empty"
	case name == "." || name == "..":
		return "the name is a path"
	case name == ".worktrees":
		return "the name is git-work's reserved worktree directory"
	case strings.HasPrefix(name, "-"):
		return "the name starts with a dash, which reads as a flag"
	case strings.ContainsAny(name, `/\`):
		return "the name contains a path separator"
	}
	return ""
}
