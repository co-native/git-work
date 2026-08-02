package repo

import "testing"

func TestParseCloneArgs(t *testing.T) {
	cases := []struct {
		args     []string
		wantRepo string
		wantDir  string
		wantOpts []string
	}{
		{[]string{"git@h:o/r.git"}, "git@h:o/r.git", "", nil},
		{[]string{"--depth", "1", "--", "url", "mydir"}, "url", "mydir", []string{"--depth", "1"}},
		{[]string{"url", "mydir"}, "url", "mydir", nil},
	}
	for _, c := range cases {
		opts, repo, dir, err := parseCloneArgs(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if repo != c.wantRepo || dir != c.wantDir {
			t.Errorf("%v -> repo=%q dir=%q", c.args, repo, dir)
		}
		if len(opts) != len(c.wantOpts) {
			t.Errorf("%v -> opts=%v", c.args, opts)
		}
	}
}

func TestParseCloneArgsRejectsOptionsWithoutSeparator(t *testing.T) {
	if _, _, _, err := parseCloneArgs([]string{"--depth", "1", "url"}); err == nil {
		t.Error("expected error for options without -- separator")
	}
}

func TestParseCloneArgsRejectsSecondURL(t *testing.T) {
	cases := [][]string{
		{"git@h:o/r1.git", "git@h:o/r2.git"},
		{"https://h/o/r1.git", "https://h/o/r2.git"},
		{"https://h/o/r1.git", "r2.git"},
		{"--depth", "1", "--", "git@h:o/r1.git", "git@h:o/r2.git"},
	}
	for _, args := range cases {
		if _, _, _, err := parseCloneArgs(args); err == nil {
			t.Errorf("%v: expected error for URL-looking <dir>", args)
		}
	}
	// A plain directory name must still be accepted.
	if _, _, dir, err := parseCloneArgs([]string{"git@h:o/r.git", "mydir"}); err != nil || dir != "mydir" {
		t.Errorf("plain dir rejected: dir=%q err=%v", dir, err)
	}
}

func TestDeriveDir(t *testing.T) {
	cases := map[string]string{
		"git@github.com:org/myrepo.git":     "myrepo",
		"https://github.com/org/myrepo.git": "myrepo",
		"https://github.com/org/myrepo":     "myrepo",
		"/local/path/myrepo":                "myrepo",
	}
	for in, want := range cases {
		if got := deriveDir(in); got != want {
			t.Errorf("deriveDir(%q) = %q want %q", in, got, want)
		}
	}
}
