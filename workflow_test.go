package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarkLedgerEntriesMergedMarksOnlySelectedEntries(t *testing.T) {
	cacheDir := t.TempDir()
	repoURL := "git@github.com:example/repo.git"
	skillPath := "skills/sample/SKILL.md"
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	entries := []LedgerEntry{
		{ID: "first", RepoURL: repoURL, SkillPath: skillPath, Learnings: []string{"one"}, Timestamp: now},
		{ID: "second", RepoURL: repoURL, SkillPath: skillPath, Learnings: []string{"two"}, Timestamp: now.Add(time.Minute)},
		{ID: "third", RepoURL: repoURL, SkillPath: skillPath, Learnings: []string{"three"}, Timestamp: now.Add(2 * time.Minute)},
	}
	for _, entry := range entries {
		if err := WriteLedger(cacheDir, entry); err != nil {
			t.Fatalf("WriteLedger returned error: %v", err)
		}
	}

	if err := MarkLedgerEntriesMerged(cacheDir, repoURL, skillPath, []string{"first", "third"}, "abc123"); err != nil {
		t.Fatalf("MarkLedgerEntriesMerged returned error: %v", err)
	}

	got, err := ReadLedger(cacheDir, repoURL, skillPath, 0)
	if err != nil {
		t.Fatalf("ReadLedger returned error: %v", err)
	}

	byID := make(map[string]LedgerEntry, len(got))
	for _, entry := range got {
		byID[entry.ID] = entry
	}

	if byID["first"].CommitSHA != "abc123" {
		t.Fatalf("expected first entry to be merged, got %q", byID["first"].CommitSHA)
	}
	if byID["third"].CommitSHA != "abc123" {
		t.Fatalf("expected third entry to be merged, got %q", byID["third"].CommitSHA)
	}
	if byID["second"].CommitSHA != "" {
		t.Fatalf("expected second entry to remain unmerged, got %q", byID["second"].CommitSHA)
	}
}

func TestValidateMergedContent(t *testing.T) {
	original := "---\ndescription: Sample\n---\n\nBody\n"

	if err := validateMergedContent(original, ""); err == nil {
		t.Fatalf("expected empty output to fail validation")
	}
	if err := validateMergedContent(original, "```md\nBody\n```"); err == nil {
		t.Fatalf("expected fenced output to fail validation")
	}
	if err := validateMergedContent(original, "Body only"); err == nil {
		t.Fatalf("expected missing frontmatter to fail validation")
	}
	if err := validateMergedContent(original, "---\ndescription: Sample\n---\n\nUpdated body\n"); err != nil {
		t.Fatalf("expected valid merged content, got error: %v", err)
	}
}

func TestLoadRegisteredSkill(t *testing.T) {
	repoDir := createTestGitRepo(t, map[string]string{
		"skills/sample/SKILL.md": "---\ndescription: Sample skill\n---\n\nBody\n",
	})
	cacheDir := t.TempDir()

	loaded, err := loadRegisteredSkill(cacheDir, RegisteredSkill{
		Name:      "sample",
		RepoURL:   repoDir,
		SkillPath: "skills/sample/SKILL.md",
	})
	if err != nil {
		t.Fatalf("loadRegisteredSkill returned error: %v", err)
	}
	if !loaded.FileExists {
		t.Fatalf("expected skill file to exist")
	}
	if loaded.Description != "Sample skill" {
		t.Fatalf("unexpected description: %q", loaded.Description)
	}
	if !strings.Contains(loaded.Content, "Body") {
		t.Fatalf("expected content to be loaded, got %q", loaded.Content)
	}
}

func TestLoadRegisteredSkillMarksMissingFile(t *testing.T) {
	repoDir := createTestGitRepo(t, map[string]string{
		"README.md": "hello\n",
	})
	cacheDir := t.TempDir()

	loaded, err := loadRegisteredSkill(cacheDir, RegisteredSkill{
		Name:      "sample",
		RepoURL:   repoDir,
		SkillPath: "skills/missing/SKILL.md",
	})
	if err != nil {
		t.Fatalf("expected missing skill file to be treated as new, got error: %v", err)
	}
	if loaded.FileExists {
		t.Fatalf("expected missing skill file to be reported as absent")
	}
}

func TestNormalizeToolDescription(t *testing.T) {
	got := normalizeToolDescription("```text\n  \"Sample guide for deployment workflows\"  \n```")
	if got != "Sample guide for deployment workflows" {
		t.Fatalf("unexpected normalized description: %q", got)
	}
}

func TestResolveSessionBySkillCreatesCachedRepo(t *testing.T) {
	repoDir := createTestGitRepo(t, map[string]string{
		"skills/sample/SKILL.md": "---\ndescription: Sample skill\n---\n\nBody\n",
	})
	cacheDir := t.TempDir()
	cfg := &SkillConfig{
		Skills: []RegisteredSkill{{
			Name:      "sample",
			RepoURL:   repoDir,
			SkillPath: "skills/sample/SKILL.md",
		}},
	}
	sessions := NewSessionManager()

	session, err := resolveSession(sessions, cfg, cacheDir, "", "sample")
	if err != nil {
		t.Fatalf("resolveSession returned error: %v", err)
	}
	if !strings.Contains(session.OrigContent, "Body") {
		t.Fatalf("expected original content to be loaded, got %q", session.OrigContent)
	}
	if _, err := os.Stat(filepath.Join(session.LocalRepoPath, ".git")); err != nil {
		t.Fatalf("expected cached repo to exist, stat returned: %v", err)
	}
}

func createTestGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	repoDir := t.TempDir()
	runCommand(t, exec.Command("git", "init", repoDir))
	runCommand(t, exec.Command("git", "-C", repoDir, "config", "user.name", "Test User"))
	runCommand(t, exec.Command("git", "-C", repoDir, "config", "user.email", "test@example.com"))

	for path, content := range files {
		fullPath := filepath.Join(repoDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	runCommand(t, exec.Command("git", "-C", repoDir, "add", "."))
	runCommand(t, exec.Command("git", "-C", repoDir, "commit", "-m", "initial"))
	return repoDir
}

func runCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%q failed: %v\n%s", strings.Join(cmd.Args, " "), err, string(out))
	}
}
