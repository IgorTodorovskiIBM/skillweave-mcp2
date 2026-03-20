package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Parameter structs ---

type SkillUpdateParams struct {
	SessionID      string   `json:"session_id,omitempty" jsonschema:"session ID returned when the skill was loaded (optional if skill_name is provided)"`
	SkillName      string   `json:"skill_name,omitempty" jsonschema:"skill name as fallback when session_id is unavailable"`
	Learnings      []string `json:"learnings" jsonschema:"list of things learned this session (corrections, tips, patterns, warnings)"`
	UpdatedContent string   `json:"updated_content" jsonschema:"full new SKILL.md content with learnings incorporated"`
}

type SkillPushParams struct {
	SessionID     string `json:"session_id,omitempty" jsonschema:"session ID returned when the skill was loaded (optional if skill_name is provided)"`
	SkillName     string `json:"skill_name,omitempty" jsonschema:"skill name as fallback when session_id is unavailable"`
	CommitMessage string `json:"commit_message" jsonschema:"commit message for the update"`
	SkipPR        bool   `json:"skip_pr,omitempty" jsonschema:"set true to push branch only without creating a PR (default false)"`
}

// resolveSession tries session_id first, then falls back to skill_name.
// When falling back to a registered skill, it ensures the cached repo exists
// before creating an ad-hoc session.
func resolveSession(sessions *SessionManager, cfg *SkillConfig, cacheDir, sessionID, skillName string) (*Session, error) {
	if sessionID != "" {
		if s, err := sessions.Get(sessionID); err == nil {
			return s, nil
		}
	}
	if skillName == "" {
		return nil, fmt.Errorf("provide session_id or skill_name")
	}
	if s, err := sessions.FindBySkillName(skillName); err == nil {
		return s, nil
	}

	skill, err := cfg.FindSkill(skillName)
	if err != nil {
		return nil, err
	}
	localRepoPath, err := ensureRepo(skill.RepoURL, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("fetch repo: %w", err)
	}
	content, err := os.ReadFile(filepath.Join(localRepoPath, skill.SkillPath))
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	var localFilePath string
	if skill.LocalPath != "" {
		lp := filepath.Join(skill.LocalPath, skill.SkillPath)
		if _, err := os.Stat(lp); err == nil {
			localFilePath = lp
		}
	}

	return sessions.Create(skill.Name, skill.RepoURL, skill.SkillPath, localRepoPath, localFilePath, string(content)), nil
}

func sessionLearningEntryIDs(cacheDir string, session *Session) ([]string, error) {
	entries, err := ReadLedger(cacheDir, session.RepoURL, session.SkillPath, 0)
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		if e.SessionID != session.ID || e.CommitSHA != "" || len(e.Learnings) == 0 {
			continue
		}
		ids = append(ids, e.ID)
	}
	return ids, nil
}

// registerTools registers all MCP tools on the server.
func registerTools(srv *mcp.Server, sessions *SessionManager, cfg *SkillConfig, cacheDir string) {

	// --- Dynamic skill tools (one per registered skill) ---
	for _, s := range cfg.Skills {
		s := s // capture for closure

		// Ensure we have a cached clone, then check for updates.
		desc := "Skill guide: " + s.Name
		localRepoPath := repoCacheDir(cacheDir, s.RepoURL)
		skillFile := filepath.Join(localRepoPath, s.SkillPath)

		if _, err := os.Stat(filepath.Join(localRepoPath, ".git")); err != nil {
			// No cache — clone once.
			if clonedPath, err := ensureRepo(s.RepoURL, cacheDir); err == nil {
				localRepoPath = clonedPath
				skillFile = filepath.Join(localRepoPath, s.SkillPath)
			} else {
				fmt.Fprintf(os.Stderr, "warning: could not clone %s for skill %q: %v\n", s.RepoURL, s.Name, err)
			}
		} else {
			// Cache exists — quick check if remote has updates.
			if updated, err := checkForUpdates(localRepoPath, s.SkillPath); err == nil && updated {
				fmt.Fprintf(os.Stderr, "skill %q: remote has updates, pulling latest\n", s.Name)
				ensureRepo(s.RepoURL, cacheDir)
			}
		}

		if raw, err := os.ReadFile(skillFile); err == nil {
			_, frontDesc, _ := parseFrontmatter(string(raw))
			if frontDesc != "" {
				desc = frontDesc
			}
		}

		toolName := "skill_" + strings.ReplaceAll(s.Name, "-", "_")

		mcp.AddTool(srv, &mcp.Tool{
			Name:        toolName,
			Description: desc,
		}, func(ctx context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			// Fetch latest on each call.
			localPath, err := ensureRepo(s.RepoURL, cacheDir)
			if err != nil {
				return textResult("Error fetching repo: " + err.Error()), map[string]any{}, nil
			}

			content, err := os.ReadFile(filepath.Join(localPath, s.SkillPath))
			if err != nil {
				return textResult("Error reading SKILL.md: " + err.Error()), map[string]any{}, nil
			}

			// Determine local checkout path.
			var localFilePath string
			if s.LocalPath != "" {
				lp := filepath.Join(s.LocalPath, s.SkillPath)
				if _, err := os.Stat(lp); err == nil {
					localFilePath = lp
				}
			}

			session := sessions.Create(s.Name, s.RepoURL, s.SkillPath, localPath, localFilePath, string(content))

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("session_id: %s\n", session.ID))
			sb.WriteString(fmt.Sprintf("skill: %s\n", s.Name))
			sb.WriteString("\n---\n")
			sb.WriteString(string(content))

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
			}, map[string]any{}, nil
		})
	}

	// --- skill_update ---
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_update",
		Description: "Save an updated SKILL.md locally. Call this when the user has corrected you multiple times, you discovered a new pattern or fix, the user asks you to update the skill, or the session is ending with meaningful learnings. Pass your learnings as a list and the full updated SKILL.md content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillUpdateParams) (*mcp.CallToolResult, map[string]any, error) {
		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}
		if err := validateMergedContent(session.OrigContent, in.UpdatedContent); err != nil {
			return textResult("Error: invalid updated content: " + err.Error()), map[string]any{}, nil
		}

		// Always write to the cache repo.
		cachePath := filepath.Join(session.LocalRepoPath, session.SkillPath)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return textResult("Error creating directory: " + err.Error()), map[string]any{}, nil
		}
		if err := os.WriteFile(cachePath, []byte(in.UpdatedContent), 0o644); err != nil {
			return textResult("Error writing to cache: " + err.Error()), map[string]any{}, nil
		}

		// Also write to local checkout if registered.
		var localMsg string
		if session.LocalFilePath != "" {
			if err := os.MkdirAll(filepath.Dir(session.LocalFilePath), 0o755); err != nil {
				localMsg = fmt.Sprintf("\nWarning: could not write to local path: %v", err)
			} else if err := os.WriteFile(session.LocalFilePath, []byte(in.UpdatedContent), 0o644); err != nil {
				localMsg = fmt.Sprintf("\nWarning: could not write to local path: %v", err)
			} else {
				localMsg = fmt.Sprintf("\nAlso written to: %s", session.LocalFilePath)
			}
		}

		// Write ledger entry.
		entry := LedgerEntry{
			ID:        generateID(),
			SessionID: session.ID,
			RepoURL:   session.RepoURL,
			SkillPath: session.SkillPath,
			Learnings: in.Learnings,
			Timestamp: time.Now(),
		}
		if err := WriteLedger(cacheDir, entry); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write ledger: %v\n", err)
		}

		session.Saved = true

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Skill %q updated locally.", session.SkillName))
		sb.WriteString(fmt.Sprintf("\nLearnings recorded: %d", len(in.Learnings)))
		sb.WriteString(localMsg)
		sb.WriteString("\n\nUse skill_push to create a PR when ready to share with the team.")

		return textResult(sb.String()), map[string]any{}, nil
	})

	// --- skill_push ---
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_push",
		Description: "Push the updated SKILL.md to GitHub as a PR. Call skill_update first to save locally, then call this when the user wants to share the changes with the team.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillPushParams) (*mcp.CallToolResult, map[string]any, error) {
		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}

		if !session.Saved {
			return textResult("Error: nothing to push. Call skill_update first to save changes. Use the CLI 'skillweave push' to merge and push ledgered learnings from previous sessions."), map[string]any{}, nil
		}

		createPRFlag := !in.SkipPR

		// Read the locally saved content before fetching (fetch will reset the working tree).
		cachePath := filepath.Join(session.LocalRepoPath, session.SkillPath)
		content, err := os.ReadFile(cachePath)
		if err != nil {
			return textResult("Error reading saved file: " + err.Error()), map[string]any{}, nil
		}

		// Fetch latest from remote to ensure we branch from the current upstream.
		// This prevents overwriting changes pushed by others since the session started.
		if _, err := ensureRepo(session.RepoURL, cacheDir); err != nil {
			return textResult("Error fetching latest from remote: " + err.Error()), map[string]any{}, nil
		}
		latestContent, err := os.ReadFile(cachePath)
		if err != nil {
			return textResult("Error reading upstream file: " + err.Error()), map[string]any{}, nil
		}
		if string(content) == string(latestContent) {
			return textResult("Nothing to push. The saved SKILL.md matches the latest upstream content."), map[string]any{}, nil
		}

		branch := fmt.Sprintf("skill-update/%s/%s", session.SkillName, time.Now().Format("20060102-150405"))

		commitSHA, err := createBranchAndCommit(session.LocalRepoPath, session.SkillPath, string(content), in.CommitMessage, branch)
		if err != nil {
			return textResult("Error committing: " + err.Error()), map[string]any{}, nil
		}

		if err := push(session.LocalRepoPath, branch); err != nil {
			return textResult("Error pushing: " + err.Error()), map[string]any{}, nil
		}

		mergedEntryIDs, err := sessionLearningEntryIDs(cacheDir, session)
		if err != nil {
			return textResult("Error reading ledger: " + err.Error()), map[string]any{}, nil
		}

		var prURL string
		var prWarning string
		if createPRFlag {
			body := buildPRBody(in.CommitMessage)
			prURL, err = createPR(session.LocalRepoPath, branch, in.CommitMessage, body)
			if err != nil {
				prWarning = fmt.Sprintf(
					"\n\nPR creation failed: %s\n\n"+
						"ACTION REQUIRED: Create the PR manually:\n"+
						"  gh pr create --head %s --title %q\n\n"+
						"Common fixes:\n"+
						"  - Install gh: https://cli.github.com\n"+
						"  - Authenticate: gh auth login",
					err.Error(), branch, in.CommitMessage,
				)
			}
		}

		// Mark the learnings recorded for this session as merged, then record the push.
		if err := MarkLedgerEntriesMerged(cacheDir, session.RepoURL, session.SkillPath, mergedEntryIDs, commitSHA); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to mark ledger entries as merged: %v\n", err)
		}
		entry := LedgerEntry{
			ID:        generateID(),
			SessionID: session.ID,
			RepoURL:   session.RepoURL,
			SkillPath: session.SkillPath,
			CommitSHA: commitSHA,
			PRUrl:     prURL,
			Timestamp: time.Now(),
		}
		if err := WriteLedger(cacheDir, entry); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write ledger: %v\n", err)
		}

		sessions.Remove(session.ID)

		if prURL != "" {
			return textResult(fmt.Sprintf("PR created: %s\nCommit: %s", prURL, commitSHA)), map[string]any{}, nil
		}
		return textResult(fmt.Sprintf("Pushed to branch: %s\nCommit: %s%s", branch, commitSHA, prWarning)), map[string]any{}, nil
	})
}

// textResult creates a simple text CallToolResult.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

// buildPRBody creates a PR description.
func buildPRBody(commitMessage string) string {
	var sb strings.Builder
	sb.WriteString("## SKILL.md Update\n\n")
	sb.WriteString(commitMessage)
	sb.WriteString("\n\n---\n")
	sb.WriteString("Generated by [skillweave](https://github.com/IgorTodorovskiIBM/skillweave)\n")
	return sb.String()
}
