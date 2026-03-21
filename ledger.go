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
	repoHash := hashString(normalizeRepoURL(entry.RepoURL))
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
	repoHash := hashString(normalizeRepoURL(repoURL))
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

// DeleteLedgerEntry removes a single ledger entry by ID.
func DeleteLedgerEntry(cacheDir, repoURL, skillPath, entryID string) error {
	repoHash := hashString(normalizeRepoURL(repoURL))
	skillHash := hashString(skillPath)
	baseDir := filepath.Join(cacheDir, "ledger", repoHash, skillHash)

	var found bool
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
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
		if e.ID == entryID {
			found = true
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("ledger entry %q not found", entryID)
	}
	return nil
}

// ClearLedger removes all ledger entries for a skill.
func ClearLedger(cacheDir, repoURL, skillPath string) (int, error) {
	repoHash := hashString(normalizeRepoURL(repoURL))
	skillHash := hashString(skillPath)
	baseDir := filepath.Join(cacheDir, "ledger", repoHash, skillHash)

	var count int
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		count++
		return os.Remove(path)
	})
	return count, err
}

// MarkLedgerEntriesMerged stamps the selected learnings-only entries with the
// given commit SHA so they are not picked up as "unmerged" again.
func MarkLedgerEntriesMerged(cacheDir, repoURL, skillPath string, entryIDs []string, commitSHA string) error {
	if len(entryIDs) == 0 {
		return nil
	}

	repoHash := hashString(normalizeRepoURL(repoURL))
	skillHash := hashString(skillPath)
	baseDir := filepath.Join(cacheDir, "ledger", repoHash, skillHash)
	remaining := make(map[string]struct{}, len(entryIDs))
	for _, id := range entryIDs {
		remaining[id] = struct{}{}
	}

	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
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
		if _, ok := remaining[e.ID]; !ok {
			return nil
		}
		if e.CommitSHA != "" || len(e.Learnings) == 0 {
			delete(remaining, e.ID)
			return nil
		}
		e.CommitSHA = commitSHA
		updated, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal ledger entry %s: %w", e.ID, err)
		}
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			return fmt.Errorf("write ledger entry %s: %w", e.ID, err)
		}
		delete(remaining, e.ID)
		if len(remaining) == 0 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return err
	}
	if len(remaining) > 0 {
		missing := make([]string, 0, len(remaining))
		for id := range remaining {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return fmt.Errorf("ledger entries not found: %v", missing)
	}
	return nil
}
