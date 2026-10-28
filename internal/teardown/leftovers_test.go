package teardown

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/co-native/git-work/internal/layout"
)

func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLeftovers(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, layout.WorktreesDirName)
	mkdirs(t, root, "integrate-api", "agent-x/api", "agent-x/web", "agent-y/api")

	top, err := Leftovers(work)
	if err != nil {
		t.Fatal(err)
	}
	wantTop := []string{"agent-x", "agent-y", "integrate-api"}
	if !reflect.DeepEqual(top, wantTop) {
		t.Errorf("Leftovers = %v, want %v", top, wantTop)
	}

	api, err := RepoLeftovers(work, "api")
	if err != nil {
		t.Fatal(err)
	}
	wantAPI := []string{filepath.Join("agent-x", "api"), filepath.Join("agent-y", "api"), "integrate-api"}
	if !reflect.DeepEqual(api, wantAPI) {
		t.Errorf("RepoLeftovers(api) = %v, want %v", api, wantAPI)
	}

	web, err := RepoLeftovers(work, "web")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join("agent-x", "web")}; !reflect.DeepEqual(web, want) {
		t.Errorf("RepoLeftovers(web) = %v, want %v", web, want)
	}

	if err := RemoveLeftovers(work, api); err != nil {
		t.Fatal(err)
	}
	top, err = Leftovers(work)
	if err != nil {
		t.Fatal(err)
	}
	// agent-y is now empty and pruned; agent-x still holds web.
	if len(top) != 1 || top[0] != "agent-x" {
		t.Errorf("Leftovers after remove = %v, want agent-x only", top)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-y")); !os.IsNotExist(err) {
		t.Error("empty agent-y directory was not pruned")
	}
	if _, err := os.Stat(filepath.Join(root, "agent-x", "web")); err != nil {
		t.Errorf("web entry should survive: %v", err)
	}
}
