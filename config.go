package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RegisteredSkill is a skill tracked by the server.
type RegisteredSkill struct {
	Name      string `json:"name"`
	RepoURL   string `json:"repo_url"`
	SkillPath string `json:"skill_path"`
	LocalPath string `json:"local_path,omitempty"` // Local checkout root (e.g. /Users/igor/Projects/zos-porting)
}

// SkillConfig holds all registered skills.
type SkillConfig struct {
	Skills []RegisteredSkill `json:"skills"`
}

func configPath(cacheDir string) string {
	return filepath.Join(cacheDir, "skills.json")
}

// LoadConfig reads the skill registry from disk.
func LoadConfig(cacheDir string) (*SkillConfig, error) {
	path := configPath(cacheDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SkillConfig{}, nil
		}
		return nil, err
	}
	var cfg SkillConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// SaveConfig writes the skill registry to disk.
func SaveConfig(cacheDir string, cfg *SkillConfig) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(cacheDir), append(data, '\n'), 0o644)
}

// FindSkill looks up a registered skill by name.
func (c *SkillConfig) FindSkill(name string) (*RegisteredSkill, error) {
	for i := range c.Skills {
		if c.Skills[i].Name == name {
			return &c.Skills[i], nil
		}
	}
	return nil, fmt.Errorf("skill not registered: %q (use 'skillweave register' to add it)", name)
}

// AddSkill registers a new skill, replacing any existing one with the same name.
func (c *SkillConfig) AddSkill(s RegisteredSkill) {
	for i := range c.Skills {
		if c.Skills[i].Name == s.Name {
			c.Skills[i] = s
			return
		}
	}
	c.Skills = append(c.Skills, s)
}

// RemoveSkill removes a skill by name. Returns true if found.
func (c *SkillConfig) RemoveSkill(name string) bool {
	for i := range c.Skills {
		if c.Skills[i].Name == name {
			c.Skills = append(c.Skills[:i], c.Skills[i+1:]...)
			return true
		}
	}
	return false
}

// githubBlobRe matches: https://github.com/<owner>/<repo>/blob/<branch>/<path>
var githubBlobRe = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/blob/[^/]+/(.+)$`)

// ParseGitHubURL extracts repo URL and file path from a GitHub blob URL.
func ParseGitHubURL(rawURL string) (repoURL, skillPath string, err error) {
	m := githubBlobRe.FindStringSubmatch(rawURL)
	if m == nil {
		return "", "", fmt.Errorf("not a GitHub blob URL: %s\nExpected: https://github.com/<owner>/<repo>/blob/<branch>/<path-to-SKILL.md>", rawURL)
	}
	repoURL = "https://github.com/" + m[1] + ".git"
	skillPath = m[2]
	return repoURL, skillPath, nil
}

// DeriveSkillName guesses a short name from the skill path.
// "skills/zos-porting-cli/SKILL.md" → "zos-porting-cli"
// "SKILL.md" → "default"
func DeriveSkillName(skillPath string) string {
	dir := filepath.Dir(skillPath)
	if dir == "." || dir == "" {
		return "default"
	}
	return filepath.Base(dir)
}

// parseFrontmatter extracts name and description from a YAML frontmatter block
// delimited by "---" lines. Returns (name, description, body).
func parseFrontmatter(raw string) (string, string, string) {
	const delim = "---"

	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, delim) {
		return "", "", raw
	}

	rest := trimmed[len(delim):]
	idx := strings.Index(rest, "\n"+delim)
	if idx < 0 {
		return "", "", raw
	}

	frontmatter := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n"+delim):])

	var name, desc string
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		switch key {
		case "name":
			name = val
		case "description":
			desc = val
		}
	}
	return name, desc, body
}

// FormatSkillList returns a human-readable list of registered skills.
func FormatSkillList(cfg *SkillConfig) string {
	if len(cfg.Skills) == 0 {
		return "No skills registered. Use 'skillweave register <github-url>' to add one."
	}
	var sb strings.Builder
	for i, s := range cfg.Skills {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("  %s\n    repo:  %s\n    path:  %s", s.Name, s.RepoURL, s.SkillPath))
		if s.LocalPath != "" {
			sb.WriteString(fmt.Sprintf("\n    local: %s", filepath.Join(s.LocalPath, s.SkillPath)))
		}
	}
	return sb.String()
}
