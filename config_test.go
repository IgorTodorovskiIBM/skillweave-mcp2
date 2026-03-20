package main

import "testing"

func TestParseGitHubURLBlob(t *testing.T) {
	repoURL, skillPath, err := ParseGitHubURL("https://github.com/example/repo/blob/main/skills/sample/SKILL.md")
	if err != nil {
		t.Fatalf("ParseGitHubURL returned error: %v", err)
	}
	if repoURL != "git@github.com:example/repo.git" {
		t.Fatalf("unexpected repo URL: %s", repoURL)
	}
	if skillPath != "skills/sample/SKILL.md" {
		t.Fatalf("unexpected skill path: %s", skillPath)
	}
}

func TestParseGitHubURLSlashBranchWithSkillsPath(t *testing.T) {
	repoURL, skillPath, err := ParseGitHubURL("https://github.com/example/repo/blob/feature/test/skills/sample/SKILL.md")
	if err != nil {
		t.Fatalf("ParseGitHubURL returned error: %v", err)
	}
	if repoURL != "git@github.com:example/repo.git" {
		t.Fatalf("unexpected repo URL: %s", repoURL)
	}
	if skillPath != "skills/sample/SKILL.md" {
		t.Fatalf("unexpected skill path: %s", skillPath)
	}
}

func TestParseGitHubURLRawSlashBranchWithSkillsPath(t *testing.T) {
	repoURL, skillPath, err := ParseGitHubURL("https://raw.githubusercontent.com/example/repo/feature/test/skills/sample/SKILL.md")
	if err != nil {
		t.Fatalf("ParseGitHubURL returned error: %v", err)
	}
	if repoURL != "git@github.com:example/repo.git" {
		t.Fatalf("unexpected repo URL: %s", repoURL)
	}
	if skillPath != "skills/sample/SKILL.md" {
		t.Fatalf("unexpected skill path: %s", skillPath)
	}
}
