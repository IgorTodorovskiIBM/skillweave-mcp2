package main

import (
	"crypto/sha256"
	"errors"
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
	logger := GetLogger().WithFields(map[string]interface{}{
		"repo_url": repoURL,
		"operation": "ensure_repo",
	})
	logger.Debug("ensuring repository is cached")
	
	localPath := repoCacheDir(cacheDir, repoURL)

	if _, err := os.Stat(filepath.Join(localPath, ".git")); err == nil {
		logger.Debug("repository cache exists, fetching updates")
		// Repo exists — fetch and reset to origin default branch.
		cmd := exec.Command("git", "-C", localPath, "fetch", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.WithError(err).Error("git fetch failed")
			return "", WrapErrorWithFields("git fetch", err, map[string]interface{}{
				"output": string(out),
				"local_path": localPath,
			})
		}
		logger.Debug("git fetch completed successfully")

		// Determine default branch.
		branch, err := defaultBranch(localPath)
		if err != nil {
			logger.WithError(err).Error("failed to determine default branch")
			return "", WrapError("determine default branch", err)
		}
		logger.WithField("branch", branch).Debug("determined default branch")

		// Clean up any uncommitted changes and stale branches.
		exec.Command("git", "-C", localPath, "checkout", "--", ".").Run()
		cmd = exec.Command("git", "-C", localPath, "checkout", branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.WithError(err).Error("git checkout failed")
			return "", WrapErrorWithFields("git checkout", err, map[string]interface{}{
				"output": string(out),
				"branch": branch,
			})
		}
		logger.WithField("branch", branch).Debug("checked out branch")
		
		cmd = exec.Command("git", "-C", localPath, "reset", "--hard", "origin/"+branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			logger.WithError(err).Error("git reset failed")
			return "", WrapErrorWithFields("git reset", err, map[string]interface{}{
				"output": string(out),
				"branch": branch,
			})
		}
		logger.Debug("reset to origin branch")

		// Delete local skill-update branches left over from failed pushes.
		listCmd := exec.Command("git", "-C", localPath, "branch", "--list", "skill-update/*")
		if branchOut, err := listCmd.Output(); err == nil {
			branches := strings.Split(strings.TrimSpace(string(branchOut)), "\n")
			var cleaned int
			for _, b := range branches {
				b = strings.TrimSpace(b)
				if b != "" {
					exec.Command("git", "-C", localPath, "branch", "-D", b).Run()
					cleaned++
				}
			}
			if cleaned > 0 {
				logger.WithField("count", cleaned).Debug("cleaned up stale branches")
			}
		}

		logger.Info("repository cache updated successfully")
		return localPath, nil
	}

	logger.Debug("repository cache not found, cloning")
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
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff: %w", err)
}

// cloneRepo clones a fresh copy of the repo.
func cloneRepo(repoURL, localPath string) (string, error) {
	logger := GetLogger().WithFields(map[string]interface{}{
		"repo_url": repoURL,
		"local_path": localPath,
		"operation": "clone_repo",
	})
	logger.Info("cloning repository")
	
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		logger.WithError(err).Error("failed to create directory")
		return "", WrapError("mkdir", err)
	}
	
	cmd := exec.Command("git", "clone", repoURL, localPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.WithError(err).Error("git clone failed")
		return "", WrapErrorWithFields("git clone", err, map[string]interface{}{
			"output": string(out),
		})
	}
	
	logger.Info("repository cloned successfully")
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
	logger := GetLogger().WithFields(map[string]interface{}{
		"local_path": localPath,
		"branch": branch,
		"operation": "create_branch_and_commit",
	})
	logger.Info("creating branch and committing changes")
	
	defBranch, err := defaultBranch(localPath)
	if err != nil {
		logger.WithError(err).Error("failed to determine default branch")
		return "", WrapError("determine default branch", err)
	}
	logger.WithField("default_branch", defBranch).Debug("determined default branch")

	// Create and checkout new branch from default.
	cmd := exec.Command("git", "-C", localPath, "checkout", "-b", branch, "origin/"+defBranch)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.WithError(err).Error("failed to create branch")
		return "", WrapErrorWithFields("git checkout -b", err, map[string]interface{}{
			"output": string(out),
		})
	}
	logger.Debug("created and checked out new branch")

	// Write the updated SKILL.md.
	fullPath := filepath.Join(localPath, skillPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		logger.WithError(err).Error("failed to create skill directory")
		return "", WrapError("mkdir for skill", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		logger.WithError(err).Error("failed to write skill file")
		return "", WrapError("write skill", err)
	}
	logger.WithField("skill_path", skillPath).Debug("wrote skill file")

	// Stage and commit.
	cmd = exec.Command("git", "-C", localPath, "add", skillPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.WithError(err).Error("git add failed")
		return "", WrapErrorWithFields("git add", err, map[string]interface{}{
			"output": string(out),
		})
	}
	logger.Debug("staged changes")
	
	cmd = exec.Command("git", "-C", localPath, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.WithError(err).Error("git commit failed")
		return "", WrapErrorWithFields("git commit", err, map[string]interface{}{
			"output": string(out),
		})
	}
	logger.WithField("message", message).Debug("committed changes")

	// Get commit SHA.
	cmd = exec.Command("git", "-C", localPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		logger.WithError(err).Error("failed to get commit SHA")
		return "", WrapError("rev-parse", err)
	}
	commitSHA := strings.TrimSpace(string(out))
	logger.WithField("commit_sha", commitSHA).Info("successfully created commit")
	return commitSHA, nil
}

// push pushes a branch to origin.
func push(localPath, branch string) error {
	logger := GetLogger().WithFields(map[string]interface{}{
		"local_path": localPath,
		"branch": branch,
		"operation": "push",
	})
	logger.Info("pushing branch to origin")
	
	cmd := exec.Command("git", "-C", localPath, "push", "-u", "origin", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.WithError(err).Error("git push failed")
		return WrapErrorWithFields("git push", err, map[string]interface{}{
			"output": string(out),
		})
	}
	
	logger.Info("successfully pushed branch to origin")
	return nil
}

// createPR creates a pull request using the gh CLI.
func createPR(localPath, branch, title, body string) (string, error) {
	logger := GetLogger().WithFields(map[string]interface{}{
		"local_path": localPath,
		"branch": branch,
		"title": title,
		"operation": "create_pr",
	})
	logger.Info("creating pull request")
	
	cmd := exec.Command("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--head", branch,
	)
	cmd.Dir = localPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.WithError(err).Error("failed to create pull request")
		return "", WrapErrorWithFields("gh pr create", err, map[string]interface{}{
			"output": string(out),
		})
	}
	
	prURL := strings.TrimSpace(string(out))
	logger.WithField("pr_url", prURL).Info("pull request created successfully")
	return prURL, nil
}
