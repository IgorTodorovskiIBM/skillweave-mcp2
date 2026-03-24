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

type SkillNoteParams struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID returned when the skill was loaded (optional if skill_name is provided)"`
	SkillName string `json:"skill_name,omitempty" jsonschema:"skill name as fallback when session_id is unavailable"`
	Note      string `json:"note" jsonschema:"one-line description of what was learned or corrected"`
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
		if os.IsNotExist(err) {
			content = []byte("")
		} else {
			return nil, fmt.Errorf("read SKILL.md: %w", err)
		}
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

// registerTools registers all MCP tools on the server.
func registerTools(srv *mcp.Server, sessions *SessionManager, cfg *SkillConfig, cacheDir string) {
	logger := GetLogger()
	logger.WithField("skill_count", len(cfg.Skills)).Info("registering MCP tools")

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
			toolLogger := logger.WithFields(map[string]interface{}{
				"tool": toolName,
				"skill_name": s.Name,
				"operation": "load_skill",
			})
			toolLogger.Info("skill tool invoked")
			
			// Fetch latest on each call.
			localPath, err := ensureRepo(s.RepoURL, cacheDir)
			if err != nil {
				toolLogger.WithError(err).Error("failed to fetch repository")
				return textResult("Error fetching repo: " + err.Error()), map[string]any{}, nil
			}

			content, err := os.ReadFile(filepath.Join(localPath, s.SkillPath))
			if err != nil {
				if os.IsNotExist(err) {
					content = []byte("")
				} else {
					return textResult("Error reading SKILL.md: " + err.Error()), map[string]any{}, nil
				}
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
			toolLogger.WithField("session_id", session.ID).Info("created new session for skill")

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
		toolLogger := logger.WithFields(map[string]interface{}{
			"tool": "skill_update",
			"session_id": in.SessionID,
			"skill_name": in.SkillName,
			"learning_count": len(in.Learnings),
		})
		toolLogger.Info("skill_update tool invoked")
		
		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			toolLogger.WithError(err).Error("failed to resolve session")
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}
		toolLogger = toolLogger.WithField("resolved_skill", session.SkillName)
		if err := validateMergedContent(session.OrigContent, in.UpdatedContent); err != nil {
			toolLogger.WithError(err).Warn("content validation failed")
			return textResult("Error: invalid updated content: " + err.Error()), map[string]any{}, nil
		}
		toolLogger.Debug("content validation passed")

		// Always write to the cache repo.
		cachePath := filepath.Join(session.LocalRepoPath, session.SkillPath)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			toolLogger.WithError(err).Error("failed to create cache directory")
			return textResult("Error creating directory: " + err.Error()), map[string]any{}, nil
		}
		if err := os.WriteFile(cachePath, []byte(in.UpdatedContent), 0o644); err != nil {
			toolLogger.WithError(err).Error("failed to write to cache")
			return textResult("Error writing to cache: " + err.Error()), map[string]any{}, nil
		}
		toolLogger.WithField("cache_path", cachePath).Debug("wrote skill to cache")

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
			toolLogger.WithError(err).Error("failed to write ledger")
			return textResult("Error saving learnings: " + err.Error()), map[string]any{}, nil
		}

		session.SetSaved(true)
		toolLogger.WithFields(map[string]interface{}{
			"session_id": session.ID,
			"learning_count": len(in.Learnings),
		}).Info("skill updated successfully")

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Skill %q updated locally.", session.SkillName))
		sb.WriteString(fmt.Sprintf("\nLearnings recorded: %d", len(in.Learnings)))
		sb.WriteString(localMsg)
		sb.WriteString("\n\nUse skill_push to create a PR when ready to share with the team.")

		return textResult(sb.String()), map[string]any{}, nil
	})

	// --- skill_note ---
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_note",
		Description: "Quickly jot down a learning or correction. Much lighter than skill_update — just pass a one-line note. Notes are saved to the ledger and merged into SKILL.md at push time. Call this immediately when you get corrected or discover something new.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillNoteParams) (*mcp.CallToolResult, map[string]any, error) {
		toolLogger := logger.WithFields(map[string]interface{}{
			"tool":       "skill_note",
			"session_id": in.SessionID,
			"skill_name": in.SkillName,
		})
		toolLogger.Info("skill_note tool invoked")

		if strings.TrimSpace(in.Note) == "" {
			return textResult("Error: note cannot be empty"), map[string]any{}, nil
		}

		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			toolLogger.WithError(err).Error("failed to resolve session")
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}
		toolLogger = toolLogger.WithField("resolved_skill", session.SkillName)

		// Write directly to ledger as a single learning.
		entry := LedgerEntry{
			ID:        generateID(),
			SessionID: session.ID,
			RepoURL:   session.RepoURL,
			SkillPath: session.SkillPath,
			Learnings: []string{strings.TrimSpace(in.Note)},
			Timestamp: time.Now(),
		}
		if err := WriteLedger(cacheDir, entry); err != nil {
			toolLogger.WithError(err).Error("failed to write ledger")
			return textResult("Error saving note: " + err.Error()), map[string]any{}, nil
		}

		count := session.IncrementNoteCount()
		toolLogger.WithField("note_count", count).Info("note recorded")

		return textResult(fmt.Sprintf("Noted. (%d note(s) this session for %q)", count, session.SkillName)), map[string]any{}, nil
	})

	// --- skill_read ---
	type SkillReadParams struct {
		SessionID string `json:"session_id,omitempty" jsonschema:"session ID returned when the skill was loaded (optional if skill_name is provided)"`
		SkillName string `json:"skill_name,omitempty" jsonschema:"skill name as fallback when session_id is unavailable"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_read",
		Description: "Re-read the current SKILL.md content without creating a new session. Use this when you need to see the skill content again (e.g., before calling skill_update to incorporate notes).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillReadParams) (*mcp.CallToolResult, map[string]any, error) {
		toolLogger := logger.WithFields(map[string]interface{}{
			"tool":       "skill_read",
			"session_id": in.SessionID,
			"skill_name": in.SkillName,
		})
		toolLogger.Info("skill_read tool invoked")

		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			toolLogger.WithError(err).Error("failed to resolve session")
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}

		content, err := os.ReadFile(filepath.Join(session.LocalRepoPath, session.SkillPath))
		if err != nil {
			return textResult("Error reading SKILL.md: " + err.Error()), map[string]any{}, nil
		}

		return textResult(string(content)), map[string]any{}, nil
	})

	// --- skill_list_notes ---
	type SkillListNotesParams struct {
		SessionID string `json:"session_id,omitempty" jsonschema:"session ID returned when the skill was loaded (optional if skill_name is provided)"`
		SkillName string `json:"skill_name,omitempty" jsonschema:"skill name as fallback when session_id is unavailable"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_list_notes",
		Description: "List all unmerged notes for a skill. Use this to review what has been captured before pushing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillListNotesParams) (*mcp.CallToolResult, map[string]any, error) {
		toolLogger := logger.WithFields(map[string]interface{}{
			"tool":       "skill_list_notes",
			"session_id": in.SessionID,
			"skill_name": in.SkillName,
		})
		toolLogger.Info("skill_list_notes tool invoked")

		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			toolLogger.WithError(err).Error("failed to resolve session")
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}

		entries, err := ReadLedger(cacheDir, session.RepoURL, session.SkillPath, 0)
		if err != nil {
			return textResult("Error reading ledger: " + err.Error()), map[string]any{}, nil
		}
		_, unmergedLearnings := collectUnmergedLearnings(entries)

		if len(unmergedLearnings) == 0 {
			return textResult("No unmerged notes."), map[string]any{}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%d unmerged note(s) for %q:\n", len(unmergedLearnings), session.SkillName))
		for _, l := range unmergedLearnings {
			sb.WriteString("  - " + l + "\n")
		}
		return textResult(sb.String()), map[string]any{}, nil
	})

	// --- skill_push ---
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "skill_push",
		Description: "Push skill updates to GitHub as a PR. Handles everything: if there are unmerged notes, they are automatically merged into the SKILL.md by an AI tool before pushing. Just call this when the user wants to share changes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SkillPushParams) (*mcp.CallToolResult, map[string]any, error) {
		toolLogger := logger.WithFields(map[string]interface{}{
			"tool": "skill_push",
			"session_id": in.SessionID,
			"skill_name": in.SkillName,
			"skip_pr": in.SkipPR,
		})
		toolLogger.Info("skill_push tool invoked")

		session, err := resolveSession(sessions, cfg, cacheDir, in.SessionID, in.SkillName)
		if err != nil {
			toolLogger.WithError(err).Error("failed to resolve session")
			return textResult("Error: " + err.Error()), map[string]any{}, nil
		}
		toolLogger = toolLogger.WithField("resolved_skill", session.SkillName)

		// Fetch latest from remote to ensure we branch from the current upstream.
		cachePath := filepath.Join(session.LocalRepoPath, session.SkillPath)
		var savedContent []byte
		if session.IsSaved() {
			// Read the locally saved content before fetching (fetch will reset the working tree).
			savedContent, err = os.ReadFile(cachePath)
			if err != nil {
				return textResult("Error reading saved file: " + err.Error()), map[string]any{}, nil
			}
		}

		if _, err := ensureRepo(session.RepoURL, cacheDir); err != nil {
			return textResult("Error fetching latest from remote: " + err.Error()), map[string]any{}, nil
		}
		upstreamContent, err := os.ReadFile(cachePath)
		if err != nil {
			if os.IsNotExist(err) {
				upstreamContent = []byte("")
			} else {
				return textResult("Error reading upstream file: " + err.Error()), map[string]any{}, nil
			}
		}

		// Determine the content to push.
		var content string
		if session.IsSaved() {
			// skill_update was called — use that content.
			content = string(savedContent)
		} else {
			// Check for unmerged notes and auto-merge with AI.
			entries, err := ReadLedger(cacheDir, session.RepoURL, session.SkillPath, 0)
			if err != nil {
				toolLogger.WithError(err).Error("failed to read ledger")
				return textResult("Error reading ledger: " + err.Error()), map[string]any{}, nil
			}
			_, unmergedLearnings := collectUnmergedLearnings(entries)
			if len(unmergedLearnings) == 0 {
				toolLogger.Warn("nothing to push")
				return textResult("Nothing to push. No skill_update and no unmerged notes."), map[string]any{}, nil
			}

			toolLogger.WithField("unmerged_count", len(unmergedLearnings)).Info("auto-merging notes with AI")
			merged, err := mergeNotesWithAI(cfg, string(upstreamContent), unmergedLearnings, os.Stderr)
			if err != nil {
				toolLogger.WithError(err).Error("AI merge failed")
				// Fall back to asking the LLM to merge manually.
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Auto-merge failed: %s\n\n", err.Error()))
				sb.WriteString("Please incorporate these notes into the skill manually via skill_update, then call skill_push again:\n")
				for _, l := range unmergedLearnings {
					sb.WriteString("  - " + l + "\n")
				}
				return textResult(sb.String()), map[string]any{}, nil
			}
			content = merged

			// Write merged content to cache so it persists.
			if err := os.WriteFile(cachePath, []byte(content), 0o644); err != nil {
				return textResult("Error writing merged content: " + err.Error()), map[string]any{}, nil
			}
		}

		if content == string(upstreamContent) {
			return textResult("Nothing to push. The SKILL.md already matches upstream."), map[string]any{}, nil
		}

		branch := fmt.Sprintf("skill-update/%s/%s", session.SkillName, time.Now().Format("20060102-150405"))

		commitSHA, err := createBranchAndCommit(session.LocalRepoPath, session.SkillPath, content, in.CommitMessage, branch)
		if err != nil {
			toolLogger.WithError(err).Error("failed to create branch and commit")
			return textResult("Error committing: " + err.Error()), map[string]any{}, nil
		}
		toolLogger.WithFields(map[string]interface{}{
			"commit_sha": commitSHA,
			"branch": branch,
		}).Info("created commit")

		if err := push(session.LocalRepoPath, branch); err != nil {
			toolLogger.WithError(err).Error("failed to push branch")
			return textResult("Error pushing: " + err.Error()), map[string]any{}, nil
		}
		toolLogger.Info("pushed branch successfully")

		// Collect ALL unmerged learnings (across sessions) so cross-session notes get marked merged too.
		allEntries, err := ReadLedger(cacheDir, session.RepoURL, session.SkillPath, 0)
		if err != nil {
			return textResult("Error reading ledger: " + err.Error()), map[string]any{}, nil
		}
		mergedEntryIDs, _ := collectUnmergedLearnings(allEntries)

		var prURL string
		var prWarning string
		if !in.SkipPR {
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
		toolLogger.WithField("session_id", session.ID).Info("removed session after successful push")

		if prURL != "" {
			toolLogger.WithField("pr_url", prURL).Info("skill push completed with PR")
			return textResult(fmt.Sprintf("PR created: %s\nCommit: %s", prURL, commitSHA)), map[string]any{}, nil
		}
		toolLogger.Info("skill push completed without PR")
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
