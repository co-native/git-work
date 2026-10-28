package repo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lsRemote runs `git ls-remote <opts> url <refs>` and returns stdout.
func lsRemote(url string, opts []string, refs ...string) (string, error) {
	args := append([]string{"ls-remote"}, opts...)
	args = append(args, url)
	args = append(args, refs...)
	cmd := exec.Command("git", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote %s: %v: %s", url, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// RemoteDefaultBranch asks url which branch HEAD points at. It fails when the
// remote does not advertise a symbolic HEAD, which is the case a config
// default_branch override exists for.
func RemoteDefaultBranch(url string) (string, error) {
	out, err := lsRemote(url, []string{"--symref"}, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		// Format: "ref: refs/heads/main\tHEAD"
		ref, _, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(ref, "ref: refs/heads/") {
			continue
		}
		return strings.TrimPrefix(ref, "ref: refs/heads/"), nil
	}
	return "", fmt.Errorf("remote %s advertises no symbolic HEAD; set repos.<name>.default_branch or point the remote's HEAD at a real branch", url)
}

// RemoteBranchExistsAt reports whether url has refs/heads/branch.
func RemoteBranchExistsAt(url, branch string) (bool, error) {
	out, err := lsRemote(url, []string{"--heads"}, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// RemoteBranchesMatching returns the branches on url whose name matches
// ticketID with the same boundary-aware, case-insensitive rule as
// BranchesMatching. All candidates are remote, since a URL has no local refs.
func RemoteBranchesMatching(url, ticketID string) ([]Candidate, error) {
	out, err := lsRemote(url, []string{"--heads"})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		const prefix = "refs/heads/"
		if strings.HasPrefix(ref, prefix) {
			names = append(names, strings.TrimPrefix(ref, prefix))
		}
	}
	return matchingBranches(names, ticketID)
}

// primaryProbe resolves branches from a local primary clone.
type primaryProbe struct{ dir string }

func (p primaryProbe) localBranchExists(branch string) (bool, error) {
	return LocalBranchExists(p.dir, branch)
}
func (p primaryProbe) remoteBranchExists(branch string) (bool, error) {
	return RemoteBranchExists(p.dir, branch)
}
func (p primaryProbe) branchesMatching(ticketID string) ([]Candidate, error) {
	return BranchesMatching(p.dir, ticketID)
}

// remoteProbe resolves branches from a remote URL without cloning.
type remoteProbe struct{ url string }

func (p remoteProbe) localBranchExists(string) (bool, error) { return false, nil }
func (p remoteProbe) remoteBranchExists(branch string) (bool, error) {
	return RemoteBranchExistsAt(p.url, branch)
}
func (p remoteProbe) branchesMatching(ticketID string) ([]Candidate, error) {
	return RemoteBranchesMatching(p.url, ticketID)
}

// CloneCheckout makes an ordinary full clone of url at dir. It is distinct
// from Clone, which builds the managed primary layout.
func CloneCheckout(url, dir string) (err error) {
	if _, serr := os.Stat(dir); serr == nil {
		return fmt.Errorf("target already exists: %s", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	cmd := exec.Command("git", "clone", url, dir)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// SwitchToBranch checks out choice's branch in an already-cloned dir.
func SwitchToBranch(dir string, choice BranchChoice) error {
	switch choice.Mode {
	case CheckoutRemote:
		// Refresh the ref in case the branch appeared between the probe and
		// the clone; a no-op when the clone already carried it.
		if _, err := git(dir, "fetch", "origin", choice.Branch); err != nil {
			return err
		}
		_, err := git(dir, "switch", "-c", choice.Branch, "--track", "origin/"+choice.Branch)
		return err
	case CheckoutNew:
		_, err := git(dir, "switch", "-c", choice.Branch)
		return err
	default:
		_, err := git(dir, "switch", choice.Branch)
		return err
	}
}
