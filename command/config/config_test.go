package config

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/co-native/git-work/internal/config"
)

// isolate points DefaultPath() at a throwaway dir for the duration of the test.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "git-work", "config.yaml")
}

func TestListShowsDefaults(t *testing.T) {
	isolate(t)
	var buf bytes.Buffer
	if err := run([]string{"list"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "paths:") || !strings.Contains(buf.String(), "repos:") {
		t.Fatalf("list output missing paths:\n%s", buf.String())
	}
}

func TestSetThenGetRoundTrip(t *testing.T) {
	isolate(t)
	if err := run([]string{"set", "paths.work", "/custom"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := run([]string{"get", "paths.work"}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "/custom" {
		t.Fatalf("get = %q", buf.String())
	}
}

func TestSetWritesFile(t *testing.T) {
	path := isolate(t)
	if err := run([]string{"set", "paths.repos", "/custom-repos"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	c, err := cfg.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Paths.Repos != "/custom-repos" {
		t.Fatalf("paths.repos not persisted: %q", c.Paths.Repos)
	}
}

func TestUnsetThroughRun(t *testing.T) {
	path := isolate(t)
	if err := run([]string{"set", "paths.work", "/tmp/custom-work"}, &bytes.Buffer{}); err != nil {
		t.Fatal("setup:", err)
	}
	if err := run([]string{"unset", "paths.work"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	c, err := cfg.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := cfg.Default().Paths.Work; c.Paths.Work != want {
		t.Fatalf("work = %q, want default %q", c.Paths.Work, want)
	}
}

func TestPathPrintsDefaultPath(t *testing.T) {
	isolate(t)
	var buf bytes.Buffer
	if err := run([]string{"path"}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != cfg.DefaultPath() {
		t.Fatalf("path = %q want %q", buf.String(), cfg.DefaultPath())
	}
}

func TestInitRefusesExisting(t *testing.T) {
	isolate(t)
	if err := run([]string{"init"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"init"}, &bytes.Buffer{}); err == nil {
		t.Fatal("second init without --force should fail")
	}
	if err := run([]string{"init", "--force"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("init --force should succeed: %v", err)
	}
}

func TestGetUnknownKeyErrors(t *testing.T) {
	isolate(t)
	if err := run([]string{"get", "paths.nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestEditRequiresTTY(t *testing.T) {
	isolate(t)
	// In `go test` stdin is not a terminal, so edit must error rather than hang.
	if err := run([]string{"edit"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected edit to require a TTY")
	}
}
