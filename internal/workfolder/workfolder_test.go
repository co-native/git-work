package workfolder

import (
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/state"
)

func TestScope(t *testing.T) {
	st := &state.State{TicketID: "T-1", Repos: []state.Repo{{Name: "a"}, {Name: "b"}}}

	all, err := Scope(st, "", true)
	if err != nil || len(all) != 2 {
		t.Fatalf("--all: got %d repos, err %v", len(all), err)
	}
	one, err := Scope(st, "b", false)
	if err != nil || len(one) != 1 || one[0].Name != "b" {
		t.Fatalf("named: got %v, err %v", one, err)
	}
	_, err = Scope(st, "c", false)
	if !cli.IsUsage(err) {
		t.Fatalf("unknown repo: err = %v, want a usage error", err)
	}
}
