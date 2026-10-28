package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LFSInstalled reports whether git-lfs is on PATH.
func LFSInstalled() bool {
	_, err := exec.LookPath("git-lfs")
	return err == nil
}

// UsesLFS reports whether dir tracks any .gitattributes with filter=lfs. It
// reads the files from the work tree, so it works even when git-lfs is not
// installed (the clone then holds pointers instead of content).
func UsesLFS(dir string) (bool, error) {
	out, err := git(dir, "ls-files", "--", "*.gitattributes", ".gitattributes")
	if err != nil {
		return false, err
	}
	for _, rel := range strings.Split(strings.TrimSpace(out), "\n") {
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "filter=lfs") {
			return true, nil
		}
	}
	return false, nil
}

// LFSInstallLocal configures the LFS filters in dir's own .git/config, so the
// clone carries its filter setup instead of depending on a global install.
func LFSInstallLocal(dir string) error {
	_, err := git(dir, "lfs", "install", "--local")
	return err
}

// LFSFetchAll downloads every LFS object reachable from every ref.
func LFSFetchAll(dir string) error {
	_, err := git(dir, "lfs", "fetch", "--all")
	return err
}

// LFSCheckout materializes LFS pointers in dir's working tree from objects
// already in its LFS store.
func LFSCheckout(dir string) error {
	_, err := git(dir, "lfs", "checkout")
	return err
}

// PrepareLFS installs the local filters when a repo uses LFS and optionally
// fetches all refs. It returns a human-readable warning when the repo uses LFS
// but git-lfs is unavailable; that is not fatal, the clone is still usable for
// content that was already checked out.
func PrepareLFS(dir string, fetchAll bool) (string, error) {
	uses, err := UsesLFS(dir)
	if err != nil || !uses {
		return "", err
	}
	if !LFSInstalled() {
		return fmt.Sprintf("repo uses Git LFS but git-lfs is not installed; checkouts may leave pointer files"), nil
	}
	if err := LFSInstallLocal(dir); err != nil {
		return "", fmt.Errorf("git lfs install --local: %w", err)
	}
	if fetchAll {
		if err := LFSFetchAll(dir); err != nil {
			return "", fmt.Errorf("git lfs fetch --all: %w", err)
		}
	}
	if err := LFSCheckout(dir); err != nil {
		return "", fmt.Errorf("git lfs checkout: %w", err)
	}
	return "", nil
}
