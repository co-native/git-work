package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/co-native/git-work/internal/fileio"
)

// FileName is the per-work state file (YAML by default).
const FileName = ".git-work.yaml"

// JSONFileName is the alternate JSON state file name FindUp/Load also accept.
const JSONFileName = ".git-work.json"

// Repo records one repo included in a work folder.
type Repo struct {
	Name string `yaml:"name" json:"name"`
	// BranchSource records whether new/add created the branch ("new") or
	// reused one that existed ("existing"). No command reads it back; it is
	// part of the state schema DESIGN.md documents, kept for the operator.
	BranchSource string `yaml:"branch_source" json:"branch_source"`
	// Branch is the branch actually checked out in this repo (the reused
	// branch when new/add matched an existing one). State files written
	// before this field existed omit it; Load falls back to the top-level
	// State.Branch.
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
}

// State is the per-work-folder metadata.
type State struct {
	TicketID string `yaml:"ticket_id" json:"ticket_id"`
	Title    string `yaml:"title" json:"title"`
	Slug     string `yaml:"slug" json:"slug"`
	Branch   string `yaml:"branch" json:"branch"`
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	// NoTicket marks a ticketless work folder (created with `new --no-ticket`):
	// there is no ticket id, no provider, and no TICKET.md; Title carries the
	// plain name and Slug its folder/branch form.
	NoTicket  bool      `yaml:"no_ticket,omitempty" json:"no_ticket,omitempty"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	Repos     []Repo    `yaml:"repos" json:"repos"`
}

// DisplayName identifies the work folder in prompts and generated headings:
// the ticket id, or the title (the plain name) for ticketless folders.
func (s *State) DisplayName() string {
	if s.TicketID != "" {
		return s.TicketID
	}
	return s.Title
}

// Save writes the state file into workDir (atomic temp+rename).
func (s *State) Save(workDir string) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	path := filepath.Join(workDir, FileName)
	data, err := fileio.Marshal(path, s)
	if err != nil {
		return err
	}
	return fileio.WriteAtomic(path, data)
}

// Load reads the state file from workDir, accepting YAML or JSON. If the YAML
// file exists, its parse result (including errors) is returned; only when the
// YAML file is absent does it fall back to the JSON file.
func Load(workDir string) (*State, error) {
	yamlPath := filepath.Join(workDir, FileName)
	if _, err := os.Stat(yamlPath); err == nil {
		return readFile(yamlPath)
	}
	return readFile(filepath.Join(workDir, JSONFileName))
}

func readFile(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := fileio.Unmarshal(path, data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Migration: state files written before Repo.Branch existed carry only
	// the top-level branch; absent per-repo branches fall back to it. For
	// repos that had reused a differently-named existing branch under the
	// old schema, this fallback is a best guess - done's existence and
	// merged checks gate any deletion against it.
	for i := range s.Repos {
		if s.Repos[i].Branch == "" {
			s.Repos[i].Branch = s.Branch
		}
	}
	return &s, nil
}

// Repo returns the named repo and whether the work folder holds it.
func (s *State) Repo(name string) (Repo, bool) {
	for _, r := range s.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return Repo{}, false
}

// RemoveRepo drops the named repo from s.Repos; returns whether it was
// present. Preserves the order of the remaining repos. Callers persist with
// s.Save(workDir).
func (s *State) RemoveRepo(name string) bool {
	for i, r := range s.Repos {
		if r.Name == name {
			s.Repos = append(s.Repos[:i], s.Repos[i+1:]...)
			return true
		}
	}
	return false
}

// FindUp walks up from start looking for a work folder (one containing a state
// file). Returns the loaded state and the work-folder dir.
func FindUp(start string) (*State, string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, "", err
	}
	for {
		for _, name := range []string{FileName, JSONFileName} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				s, err := Load(dir)
				return s, dir, err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", fmt.Errorf("not inside a git-work work folder (no %s found)", FileName)
		}
		dir = parent
	}
}
