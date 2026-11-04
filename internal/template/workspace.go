package template

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/state"
)

// workspaceExt names the VS Code workspace file git-work writes at a work
// folder's root. VS Code titles its window after the file, so it takes the
// folder's own name rather than the ticket id.
const workspaceExt = ".code-workspace"

// WorkspaceFile is the VS Code workspace file's name inside workDir.
func WorkspaceFile(workDir string) string {
	return filepath.Base(filepath.Clean(workDir)) + workspaceExt
}

// workspaceFolder is one entry of the workspace file's `folders` array.
type workspaceFolder struct {
	Path string `json:"path"`
}

// workspaceFolders is the folder list a workspace file carries: the work folder
// root - README.md, the instructions file, .scratch/ - and then one entry per
// repo checkout, in state order so it matches README.md's list.
func workspaceFolders(st *state.State) []workspaceFolder {
	folders := make([]workspaceFolder, 0, len(st.Repos)+1)
	folders = append(folders, workspaceFolder{Path: "."})
	for _, r := range st.Repos {
		folders = append(folders, workspaceFolder{Path: r.Name})
	}
	return folders
}

// WriteWorkspace writes the work folder's VS Code workspace file.
//
// Only the top-level `folders` key is git-work's. Every other byte of an
// existing file - operator settings, extension recommendations, tasks, comments,
// formatting - is preserved, so `add`/`rm`/`refresh` change the folder list and
// nothing else. That is deliberately not the managed-block scheme the other
// generated files use: a workspace file is JSONC, which has no comment marker
// that survives into a value, and its extra keys are the operator's by design.
//
// A file without a well-formed top-level object is refused rather than
// overwritten, since rewriting it would discard whatever the operator was in the
// middle of writing.
func WriteWorkspace(workDir string, st *state.State) error {
	path := filepath.Join(workDir, WorkspaceFile(workDir))
	folders := workspaceFolders(st)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, newWorkspace(folders), 0o644)
	}
	if err != nil {
		return err
	}
	merged, err := mergeWorkspace(data, folders)
	if err != nil {
		return fmt.Errorf("%s: %w; fix or delete it so git-work can write a fresh one", path, err)
	}
	if bytes.Equal(merged, data) {
		return nil
	}
	return os.WriteFile(path, merged, 0o644)
}

// newWorkspace renders the whole file, for a folder that has none yet.
func newWorkspace(folders []workspaceFolder) []byte {
	return []byte("{\n  \"folders\": " + string(renderFolders(folders, "  ")) + "\n}\n")
}

// mergeWorkspace replaces the folders value in data, or inserts the key when the
// object does not have one yet.
func mergeWorkspace(data []byte, folders []workspaceFolder) ([]byte, error) {
	start, end, found, ok := findFoldersValue(data)
	if !ok {
		return nil, errors.New("not a JSON object git-work can edit")
	}
	if !found {
		return insertFolders(data, folders)
	}
	value := renderFolders(folders, lineIndent(data, start))
	out := make([]byte, 0, len(data)-(end-start)+len(value))
	out = append(out, data[:start]...)
	out = append(out, value...)
	out = append(out, data[end:]...)
	return out, nil
}

// insertFolders puts a folders key just inside the top-level object's opening
// brace, matching the indentation of the first key already there so a
// hand-formatted file keeps its style.
func insertFolders(data []byte, folders []workspaceFolder) ([]byte, error) {
	i := skipDocumentStart(data)
	if i >= len(data) || data[i] != '{' {
		return nil, errors.New("not a JSON object git-work can edit")
	}
	indent := "  "
	comma := ""
	if next := skipBlanks(data, i+1); next < len(data) && data[next] != '}' {
		comma = ","
		if line := lineIndent(data, next); line != "" {
			indent = line
		}
	}
	insert := "\n" + indent + `"folders": ` + string(renderFolders(folders, indent)) + comma
	out := make([]byte, 0, len(data)+len(insert))
	out = append(out, data[:i+1]...)
	out = append(out, insert...)
	out = append(out, data[i+1:]...)
	return out, nil
}

// renderFolders renders the folders array, one entry per line, with entry
// indentation one level below the key that holds it.
func renderFolders(folders []workspaceFolder, indent string) []byte {
	entry := indent + indentUnit(indent)
	var b strings.Builder
	b.WriteString("[\n")
	for i, f := range folders {
		b.WriteString(entry)
		b.WriteString("{ \"path\": ")
		path, _ := json.Marshal(f.Path)
		b.Write(path)
		b.WriteString(" }")
		if i < len(folders)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(indent)
	b.WriteString("]")
	return []byte(b.String())
}

// lineIndent returns the whitespace that opens the line pos sits on.
func lineIndent(src []byte, pos int) string {
	start := bytes.LastIndexByte(src[:pos], '\n') + 1
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

// indentUnit is the unit one nesting level adds in a file indented with indent:
// a tab when the file uses tabs, two spaces otherwise. Keeping this consistent
// matters because the folders array is spliced into a file whose style is the
// operator's, not git-work's.
func indentUnit(indent string) string {
	if strings.Contains(indent, "\t") {
		return "\t"
	}
	return "  "
}

// utf8BOM is tolerated at the start of a workspace file: editors on Windows
// still write one, and refusing a file for the BOM alone would be a dead end
// for the operator.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// skipDocumentStart returns the index of the document's first significant byte,
// past any byte-order mark, whitespace and comments.
func skipDocumentStart(src []byte) int {
	i := 0
	if bytes.HasPrefix(src, utf8BOM) {
		i = len(utf8BOM)
	}
	return skipBlanks(src, i)
}

// findFoldersValue locates the top-level "folders" key's value in a JSONC
// document. ok is false when src is not a single well-formed object, which is
// the only shape git-work edits.
func findFoldersValue(src []byte) (valueStart, valueEnd int, found, ok bool) {
	i := skipDocumentStart(src)
	if i >= len(src) || src[i] != '{' {
		return 0, 0, false, false
	}
	objEnd := skipBracketed(src, i)
	if objEnd < 0 || skipBlanks(src, objEnd) != len(src) {
		return 0, 0, false, false
	}
	limit := objEnd - 1
	for i++; i < limit; {
		i = skipBlanks(src, i)
		if i >= limit {
			break
		}
		if src[i] == ',' {
			i++
			continue
		}
		if src[i] != '"' {
			return 0, 0, false, false
		}
		keyStart := i
		if i = skipQuoted(src, i); i < 0 {
			return 0, 0, false, false
		}
		key := src[keyStart:i]
		if i = skipBlanks(src, i); i >= limit || src[i] != ':' {
			return 0, 0, false, false
		}
		start := skipBlanks(src, i+1)
		end := skipValue(src, start)
		if end < 0 || end > limit {
			return 0, 0, false, false
		}
		if !found && bytes.Equal(key, []byte(`"folders"`)) {
			valueStart, valueEnd, found = start, end, true
		}
		i = end
	}
	return valueStart, valueEnd, found, true
}

// skipBlanks advances past whitespace and JSONC comments.
func skipBlanks(src []byte, i int) int {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n':
			i++
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return len(src)
			}
			i += end + 4
		default:
			return i
		}
	}
	return i
}

// skipQuoted advances past the string opening at i, or -1 when it never closes.
func skipQuoted(src []byte, i int) int {
	for i++; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// skipValue advances past the value starting at i, or -1 when it never ends.
func skipValue(src []byte, i int) int {
	if i >= len(src) {
		return -1
	}
	switch src[i] {
	case '"':
		return skipQuoted(src, i)
	case '{', '[':
		return skipBracketed(src, i)
	}
	for i < len(src) {
		switch src[i] {
		case ',', '}', ']', '/', ' ', '\t', '\r', '\n':
			return i
		}
		i++
	}
	return i
}

// skipBracketed advances past the object or array opening at i, or -1 when the
// brackets never close or close out of order.
func skipBracketed(src []byte, i int) int {
	var open []byte
	for i < len(src) {
		i = skipBlanks(src, i)
		if i >= len(src) {
			return -1
		}
		switch src[i] {
		case '"':
			if i = skipQuoted(src, i); i < 0 {
				return -1
			}
			continue
		case '{', '[':
			open = append(open, src[i])
		case '}', ']':
			want := byte('{')
			if src[i] == ']' {
				want = '['
			}
			if len(open) == 0 || open[len(open)-1] != want {
				return -1
			}
			if open = open[:len(open)-1]; len(open) == 0 {
				return i + 1
			}
		}
		i++
	}
	return -1
}
