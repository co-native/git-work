package provider

import (
	"errors"
	"testing"

	"github.com/co-native/git-work/internal/config"
)

// stubRun replaces the command runner for one test. Fetch shells out to jira
// and gh, so this runs the fetch paths against canned output.
func stubRun(t *testing.T, out []byte, err error) {
	t.Helper()
	old := run
	run = func(string, ...string) ([]byte, error) { return out, err }
	t.Cleanup(func() { run = old })
}

func TestResolveIdentityNoProviderMatch(t *testing.T) {
	noDefault := []config.Provider{{
		Name: "jira", Type: "jira",
		Patterns: []config.Pattern{{Prefix: "PROJ"}},
	}}
	in, matched, err := ResolveIdentity(noDefault, "OTHER-7", nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("matched = true, want false")
	}
	if in.DisplayID != "OTHER-7" || in.Title != "OTHER-7" {
		t.Errorf("identity = %#v, want the display id standing in for the title", in)
	}
}

func TestResolveIdentityFetchOK(t *testing.T) {
	stubRun(t, []byte(`{"fields":{"summary":"Make it faster","description":"Faster please"}}`), nil)
	in, matched, err := ResolveIdentity(providers(), "proj-10", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !matched || in.ProviderName != "jira" {
		t.Fatalf("identity = %#v, matched = %v", in, matched)
	}
	if in.DisplayID != "PROJ-10" || in.Title != "Make it faster" || in.Description != "Faster please" {
		t.Errorf("identity = %#v", in)
	}
}

func TestResolveIdentityFetchFailureKeepsTheID(t *testing.T) {
	stubRun(t, nil, errors.New("jira: not installed"))
	var warned string
	in, matched, err := ResolveIdentity(providers(), "PROJ-11", func(msg string) { warned = msg })
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Error("matched = false, want true: the provider resolved even though the fetch failed")
	}
	if warned == "" {
		t.Error("a failed fetch must be reported to warn")
	}
	// These mirror the state `new` writes: the display id stands in for the
	// title, and neither the provider name nor a description is recorded.
	if in.Title != "PROJ-11" || in.ProviderName != "" || in.Description != "" {
		t.Errorf("identity = %#v", in)
	}
}

func TestResolveIdentityInvalidProvider(t *testing.T) {
	_, _, err := ResolveIdentity([]config.Provider{{Name: "x", Type: "sideways"}}, "PROJ-1", nil)
	if err == nil {
		t.Fatal("expected a validation error")
	}
}
