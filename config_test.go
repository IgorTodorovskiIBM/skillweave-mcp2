package main

import (
	"strings"
	"testing"
)

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

func TestRegisteredSkillToolDescriptionUsesStoredDescription(t *testing.T) {
	skill := RegisteredSkill{
		Name:        "sample",
		Description: "Custom guide summary",
	}
	if got := skill.ToolDescription(); got != "Custom guide summary" {
		t.Fatalf("unexpected tool description: %q", got)
	}
}

func TestRegisteredSkillToolDescriptionFallsBackToName(t *testing.T) {
	skill := RegisteredSkill{Name: "sample"}
	if got := skill.ToolDescription(); got != "Skill guide: sample" {
		t.Fatalf("unexpected tool description fallback: %q", got)
	}
}

func TestSkeletonSKILLIncludesDescription(t *testing.T) {
	got := SkeletonSKILL("sample", "Sample guide")
	if want := "description: \"Sample guide\""; !strings.Contains(got, want) {
		t.Fatalf("expected skeleton to contain %q, got %q", want, got)
	}
}
