package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LedgerEntry is an immutable record of a skill update.
type LedgerEntry struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	RepoURL   string    `json:"repo_url"`
	SkillPath string    `json:"skill_path"`
	Learnings []string  `json:"learnings"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	PRUrl     string    `json:"pr_url,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// WriteLedger writes a ledger entry to the date-partitioned directory.
func WriteLedger(cacheDir string, entry LedgerEntry) error {
	repoHash := hashString(entry.RepoURL)
	skillHash := hashString(entry.SkillPath)
	now := entry.Timestamp

	dir := filepath.Join(cacheDir, "ledger", repoHash, skillHash,
		fmt.Sprintf("%d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()))

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create ledger dir: %w", err)
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ledger entry: %w", err)
	}

	path := filepath.Join(dir, entry.ID+".json")
	return os.WriteFile(path, data, 0o644)
}

// ReadLedger reads the most recent ledger entries for a skill.
func ReadLedger(cacheDir, repoURL, skillPath string, limit int) ([]LedgerEntry, error) {
	repoHash := hashString(repoURL)
	var baseDir string
	if skillPath != "" {
		skillHash := hashString(skillPath)
		baseDir = filepath.Join(cacheDir, "ledger", repoHash, skillHash)
	} else {
		baseDir = filepath.Join(cacheDir, "ledger", repoHash)
	}

	var entries []LedgerEntry
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var e LedgerEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk ledger: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}
