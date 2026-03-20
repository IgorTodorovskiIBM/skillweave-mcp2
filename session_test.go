package main

import (
	"testing"
	"time"
)

func TestFindBySkillNameReturnsMostRecentSession(t *testing.T) {
	sessions := NewSessionManager()

	first := sessions.Create("sample-skill", "repo-a", "SKILL.md", "/tmp/repo-a", "", "one")
	first.StartedAt = time.Now().Add(-time.Minute)
	second := sessions.Create("sample-skill", "repo-b", "SKILL.md", "/tmp/repo-b", "", "two")

	got, err := sessions.FindBySkillName("sample-skill")
	if err != nil {
		t.Fatalf("FindBySkillName returned error: %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("expected most recent session %s, got %s", second.ID, got.ID)
	}
}
