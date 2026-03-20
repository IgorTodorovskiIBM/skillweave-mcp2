package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Session tracks the state of an active skill-update session.
type Session struct {
	ID            string    `json:"id"`
	SkillName     string    `json:"skill_name"`
	RepoURL       string    `json:"repo_url"`
	SkillPath     string    `json:"skill_path"`
	LocalRepoPath string    `json:"local_repo_path"` // Cache clone path
	LocalFilePath string    `json:"local_file_path"` // Direct local checkout path (optional)
	OrigContent   string    `json:"orig_content"`    // Content at boot time
	StartedAt     time.Time `json:"started_at"`
	Saved         bool      `json:"saved"` // Whether skill_update has been called
}

// SessionManager provides thread-safe session management.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create initializes a new session and returns it.
func (m *SessionManager) Create(skillName, repoURL, skillPath, localRepoPath, localFilePath, origContent string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &Session{
		ID:            generateID(),
		SkillName:     skillName,
		RepoURL:       repoURL,
		SkillPath:     skillPath,
		LocalRepoPath: localRepoPath,
		LocalFilePath: localFilePath,
		OrigContent:   origContent,
		StartedAt:     time.Now(),
	}
	m.sessions[s.ID] = s
	return s
}

// Get returns a session by ID.
func (m *SessionManager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return s, nil
}

// FindBySkillName returns the most recent session for a skill.
func (m *SessionManager) FindBySkillName(skillName string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var latest *Session
	for _, s := range m.sessions {
		if s.SkillName != skillName {
			continue
		}
		if latest == nil || s.StartedAt.After(latest.StartedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("session not found for skill: %s", skillName)
	}
	return latest, nil
}

// Remove deletes a session.
func (m *SessionManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}
