package template

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/state"
)

func workspaceState(repos ...string) *state.State {
	st := &state.State{TicketID: "PROJ-1", Title: "Thing", Slug: "thing", Branch: "PROJ-1-thing"}
	for _, r := range repos {
		st.Repos = append(st.Repos, state.Repo{Name: r, Branch: "PROJ-1-thing"})
	}
	return st
}

// folderPaths reads the workspace file's folders through the same scanner the
// writer uses, so the assertions do not require plain JSON.
func folderPaths(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start, end, found, ok := findFoldersValue(data)
	if !ok || !found {
		t.Fatalf("%s has no usable folders key:\n%s", path, data)
	}
	var folders []workspaceFolder
	if err := json.Unmarshal(data[start:end], &folders); err != nil {
		t.Fatalf("folders value is not an array of paths: %v\n%s", err, data[start:end])
	}
	paths := make([]string, len(folders))
	for i, f := range folders {
		paths[i] = f.Path
	}
	return paths
}

func TestWriteWorkspaceFreshFile(t *testing.T) {
	work := filepath.Join(t.TempDir(), "PROJ-1-thing")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, "PROJ-1-thing.code-workspace")
	if got := strings.Join(folderPaths(t, path), ","); got != ".,alpha,beta" {
		t.Errorf("folders = %s, want the work folder root and both repos", got)
	}
}

func TestWriteWorkspaceFollowsRepoSet(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	if err := WriteWorkspace(work, workspaceState("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha", "gamma")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderPaths(t, path), ","); got != ".,alpha,gamma" {
		t.Errorf("folders = %s, want the added repo listed and the removed one gone", got)
	}
}

// The folders key is the only generated part: operator settings, extension
// recommendations, comments and indentation all survive a re-render byte for
// byte, including a comment on the folders line itself.
func TestWriteWorkspacePreservesOperatorContent(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	fixture := "{\n" +
		"\t// my window\n" +
		"\t\"folders\": [\n" +
		"\t\t{ \"path\": \"stale\" }\n" +
		"\t], // mine\n" +
		"\t\"settings\": {\n" +
		"\t\t\"files.exclude\": { \"**/.git\": true }\n" +
		"\t},\n" +
		"\t\"extensions\": { \"recommendations\": [\"golang.go\"] }\n" +
		"}\n"
	want := "{\n" +
		"\t// my window\n" +
		"\t\"folders\": [\n" +
		"\t\t{ \"path\": \".\" },\n" +
		"\t\t{ \"path\": \"alpha\" }\n" +
		"\t], // mine\n" +
		"\t\"settings\": {\n" +
		"\t\t\"files.exclude\": { \"**/.git\": true }\n" +
		"\t},\n" +
		"\t\"extensions\": { \"recommendations\": [\"golang.go\"] }\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("workspace file after re-render:\n%s\nwant:\n%s", data, want)
	}
}

func TestWriteWorkspaceInsertsMissingKey(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	fixture := "{\n  \"settings\": { \"editor.tabSize\": 4 }\n}\n"
	want := "{\n  \"folders\": [\n    { \"path\": \".\" },\n    { \"path\": \"alpha\" }\n  ],\n" +
		"  \"settings\": { \"editor.tabSize\": 4 }\n}\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("workspace file after inserting folders:\n%s\nwant:\n%s", data, want)
	}
	// A later render replaces that key rather than inserting a second one.
	if err := WriteWorkspace(work, workspaceState("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if n := strings.Count(string(data), `"folders"`); n != 1 {
		t.Errorf("folders key appears %d times after a re-render:\n%s", n, data)
	}
	if got := strings.Join(folderPaths(t, path), ","); got != ".,alpha,beta" {
		t.Errorf("folders = %s after a re-render", got)
	}
	if !strings.Contains(string(data), `"editor.tabSize": 4`) {
		t.Errorf("re-render dropped the operator's settings:\n%s", data)
	}
}

func TestWriteWorkspaceEmptyObject(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderPaths(t, path), ","); got != ".,alpha" {
		t.Errorf("folders = %s, want an empty object filled in", got)
	}
}

// A file git-work cannot make sense of is left exactly as it was: rewriting it
// would destroy what the operator was in the middle of writing.
func TestWriteWorkspaceRefusesUnreadableFile(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	fixture := "{\n  \"folders\": [ { \"path\": \"alpha\" }\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteWorkspace(work, workspaceState("alpha", "beta"))
	if err == nil {
		t.Fatal("WriteWorkspace accepted a truncated file")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "delete") {
		t.Errorf("error should name the file and a remedy, got: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != fixture {
		t.Errorf("unreadable file was modified:\n%s", data)
	}
}

func TestWriteWorkspaceLeavesMatchingFileAlone(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a file that already lists the right folders was rewritten")
	}
}

func TestWriteWorkFolderWritesWorkspace(t *testing.T) {
	work := t.TempDir()
	if err := WriteWorkFolder(work, WorkFolderOptions{ReposRoot: t.TempDir()}, workspaceState("alpha", "beta"), nil); err != nil {
		t.Fatal(err)
	}
	got := folderPaths(t, filepath.Join(work, WorkspaceFile(work)))
	if strings.Join(got, ",") != ".,alpha,beta" {
		t.Errorf("folders = %v, want the work folder root and both repos", got)
	}
}

// A byte-order mark is tolerated and left in place: a Windows editor may have
// written the file, and the folder list is still the only thing replaced.
func TestWriteWorkspaceToleratesBOM(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, WorkspaceFile(work))
	fixture := "\xEF\xBB\xBF{\n  \"folders\": [],\n  \"settings\": { \"editor.tabSize\": 4 }\n}\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspace(work, workspaceState("alpha")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		t.Errorf("byte-order mark was dropped:\n%q", data)
	}
	if got := strings.Join(folderPaths(t, path), ","); got != ".,alpha" {
		t.Errorf("folders = %s, want the list filled in", got)
	}
	if !strings.Contains(string(data), `"editor.tabSize": 4`) {
		t.Errorf("settings were dropped:\n%s", data)
	}
}
