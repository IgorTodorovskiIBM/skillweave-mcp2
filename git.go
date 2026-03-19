package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

// repoCacheDir returns the local cache path for a repo URL.
// Normalizes the URL so HTTPS and SSH variants share the same cache.
func repoCacheDir(cacheDir, repoURL string) string {
	return filepath.Join(cacheDir, "repos", hashString(normalizeRepoURL(repoURL)))
}

// ensureRepo clones or pulls a repository, returning the local path.
func ensureRepo(repoURL, cacheDir string) (string, error) {
	localPath := repoCacheDir(cacheDir, repoURL)

	if _, err := os.Stat(filepath.Join(localPath, ".git")); err == nil {
		// Repo exists — fetch and reset to origin default branch.
		cmd := exec.Command("git", "-C", localPath, "fetch", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch: %s: %w", out, err)
		}

		// Determine default branch.
		branch, err := defaultBranch(localPath)
		if err != nil {
			return "", err
		}

		// Clean up any uncommitted changes and stale branches.
		exec.Command("git", "-C", localPath, "checkout", "--", ".").Run()
		cmd = exec.Command("git", "-C", localPath, "checkout", branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git checkout: %s: %w", out, err)
		}
		cmd = exec.Command("git", "-C", localPath, "reset", "--hard", "origin/"+branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git reset: %s: %w", out, err)
		}

		// Delete local skill-update branches left over from failed pushes.
		listCmd := exec.Command("git", "-C", localPath, "branch", "--list", "skill-update/*")
		if branchOut, err := listCmd.Output(); err == nil {
			for _, b := range strings.Split(strings.TrimSpace(string(branchOut)), "\n") {
				b = strings.TrimSpace(b)
				if b != "" {
					exec.Command("git", "-C", localPath, "branch", "-D", b).Run()
				}
			}
		}

		return localPath, nil
	}

	return cloneRepo(repoURL, localPath)
}

// checkForUpdates does a lightweight fetch and checks if the skill file
// has changed on the remote. Returns true if the remote version differs.
func checkForUpdates(localPath, skillPath string) (bool, error) {
	// Fetch without merging.
	cmd := exec.Command("git", "-C", localPath, "fetch", "origin")
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git fetch: %s: %w", out, err)
	}

	branch, err := defaultBranch(localPath)
	if err != nil {
		return false, err
	}

	// Compare the skill file between HEAD and origin.
	cmd = exec.Command("git", "-C", localPath, "diff", "--quiet", "HEAD", "origin/"+branch, "--", skillPath)
	err = cmd.Run()
	if err != nil {
		// Exit code 1 means there are differences.
		return true, nil
	}
	return false, nil
}

// cloneRepo clones a fresh copy of the repo.
func cloneRepo(repoURL, localPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	cmd := exec.Command("git", "clone", repoURL, localPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone: %s: %w", out, err)
	}
	return localPath, nil
}

// defaultBranch returns the default branch name for a local repo.
func defaultBranch(localPath string) (string, error) {
	cmd := exec.Command("git", "-C", localPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	out, err := cmd.Output()
	if err != nil {
		// Fallback: try main, then master.
		for _, b := range []string{"main", "master"} {
			check := exec.Command("git", "-C", localPath, "rev-parse", "--verify", "origin/"+b)
			if check.Run() == nil {
				return b, nil
			}
		}
		return "", fmt.Errorf("cannot determine default branch")
	}
	ref := strings.TrimSpace(string(out))
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1], nil
}

// createBranchAndCommit creates a new branch from the default branch, writes the skill content, and commits.
func createBranchAndCommit(localPath, skillPath, content, message, branch string) (string, error) {
	defBranch, err := defaultBranch(localPath)
	if err != nil {
		return "", err
	}

	// Create and checkout new branch from default.
	cmd := exec.Command("git", "-C", localPath, "checkout", "-b", branch, "origin/"+defBranch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	// Write the updated SKILL.md.
	fullPath := filepath.Join(localPath, skillPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir for skill: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}

	// Stage and commit.
	cmd = exec.Command("git", "-C", localPath, "add", skillPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %s: %w", out, err)
	}
	cmd = exec.Command("git", "-C", localPath, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %s: %w", out, err)
	}

	// Get commit SHA.
	cmd = exec.Command("git", "-C", localPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// push pushes a branch to origin.
func push(localPath, branch string) error {
	cmd := exec.Command("git", "-C", localPath, "push", "-u", "origin", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", out, err)
	}
	return nil
}

// createPR creates a pull request using the gh CLI.
func createPR(localPath, branch, title, body string) (string, error) {
	cmd := exec.Command("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--head", branch,
	)
	cmd.Dir = localPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", out, err)
	}
	return strings.TrimSpace(string(out)), nil
}
