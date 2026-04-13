package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EvolveConfig controls skill auto-creation behavior
type EvolveConfig struct {
	Enabled         bool
	AutoCreateDir   string // where auto-created skills are saved
	MinMessagesForCreate int  // minimum exchange count before creating a skill
	MaxAutoSkills   int    // max number of auto-created skills
}

func DefaultEvolveConfig(skillsDir string) EvolveConfig {
	return EvolveConfig{
		Enabled:              true,
		AutoCreateDir:        filepath.Join(skillsDir, "_learned"),
		MinMessagesForCreate: 4,
		MaxAutoSkills:        50,
	}
}

// SkillCandidate is a potential skill extracted from a successful interaction
type SkillCandidate struct {
	Name        string
	Description string
	Content     string // instructions derived from the interaction
	Tags        []string
	SourceConv  string // conversation that produced this
	CreatedAt   time.Time
}

// Evolver manages skill auto-creation and improvement
type Evolver struct {
	cfg      EvolveConfig
	registry *Registry
	mu       sync.Mutex
}

func NewEvolver(cfg EvolveConfig, registry *Registry) *Evolver {
	if cfg.AutoCreateDir != "" {
		os.MkdirAll(cfg.AutoCreateDir, 0755)
	}
	return &Evolver{cfg: cfg, registry: registry}
}

// CreateFromExperience saves a new learned skill from agent experience
// Called by the agent when it detects a reusable pattern
func (e *Evolver) CreateFromExperience(candidate SkillCandidate) error {
	if !e.cfg.Enabled {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if similar skill already exists
	if existing, ok := e.registry.Get(candidate.Name); ok {
		// Improve existing skill instead
		return e.improveSkill(existing, candidate)
	}

	// Check max limit
	learned := e.countLearnedSkills()
	if learned >= e.cfg.MaxAutoSkills {
		return fmt.Errorf("max auto-created skills reached (%d)", e.cfg.MaxAutoSkills)
	}

	// Create skill directory and SKILL.md
	dir := filepath.Join(e.cfg.AutoCreateDir, sanitizeSkillName(candidate.Name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	skillContent := formatSkillMd(candidate)
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(skillContent), 0644); err != nil {
		return err
	}

	// Load into registry
	skill, err := loadSkill(path, sanitizeSkillName(candidate.Name))
	if err != nil {
		return err
	}

	e.registry.skills[skill.Name] = skill
	fmt.Printf("🧠 Auto-created skill: /%s — %s\n", skill.Command, skill.Description)

	return nil
}

// improveSkill updates an existing skill with new insights
func (e *Evolver) improveSkill(existing *Skill, candidate SkillCandidate) error {
	// Append improvement section to existing skill content
	improvement := fmt.Sprintf("\n\n## Learned Improvement (%s)\n\n%s\n",
		time.Now().Format("2006-01-02"),
		candidate.Content,
	)

	existing.Content += improvement

	// Rewrite the SKILL.md
	path := filepath.Join(existing.Dir, "SKILL.md")
	fullContent := formatSkillMdFromSkill(existing)
	if err := os.WriteFile(path, []byte(fullContent), 0644); err != nil {
		return err
	}

	fmt.Printf("📈 Improved skill: /%s\n", existing.Command)
	return nil
}

// GenerateCreatePrompt returns the system prompt injection that tells the agent
// it can create skills from experience
func (e *Evolver) GenerateCreatePrompt() string {
	if !e.cfg.Enabled {
		return ""
	}

	return `
<skill_learning>
You have the ability to learn and create reusable skills from experience.
When you successfully solve a complex or multi-step task, consider whether the approach
could be useful for future similar tasks.

To create a learned skill, include a <create_skill> block in your response:
<create_skill>
name: descriptive_skill_name
description: What this skill does
tags: tag1, tag2
---
# Skill Instructions

Step-by-step instructions that a future agent instance can follow
to accomplish this type of task.
</create_skill>

Only create skills for genuinely reusable patterns, not for one-off answers.
Good candidates: multi-step workflows, domain-specific procedures, debugging patterns.
</skill_learning>
`
}

// ParseSkillCreation checks if an agent response contains a skill creation block
func (e *Evolver) ParseSkillCreation(response string) *SkillCandidate {
	startTag := "<create_skill>"
	endTag := "</create_skill>"

	startIdx := strings.Index(response, startTag)
	if startIdx == -1 {
		return nil
	}
	endIdx := strings.Index(response, endTag)
	if endIdx == -1 {
		return nil
	}

	block := response[startIdx+len(startTag) : endIdx]
	block = strings.TrimSpace(block)

	// Parse name, description, tags from header
	candidate := &SkillCandidate{
		CreatedAt: time.Now(),
	}

	lines := strings.Split(block, "\n")
	contentStart := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "---" {
			contentStart = i + 1
			break
		}
		if strings.HasPrefix(line, "name:") {
			candidate.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
		if strings.HasPrefix(line, "description:") {
			candidate.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
		if strings.HasPrefix(line, "tags:") {
			tagStr := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			for _, t := range strings.Split(tagStr, ",") {
				candidate.Tags = append(candidate.Tags, strings.TrimSpace(t))
			}
		}
		contentStart = i + 1
	}

	if contentStart < len(lines) {
		candidate.Content = strings.TrimSpace(strings.Join(lines[contentStart:], "\n"))
	}

	if candidate.Name == "" || candidate.Content == "" {
		return nil
	}

	return candidate
}

// --- Helpers ---

func (e *Evolver) countLearnedSkills() int {
	entries, err := os.ReadDir(e.cfg.AutoCreateDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count
}

func formatSkillMd(c SkillCandidate) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", c.Name))
	sb.WriteString(fmt.Sprintf("description: %s\n", c.Description))
	if len(c.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("tags:\n"))
		for _, t := range c.Tags {
			sb.WriteString(fmt.Sprintf("  - %s\n", t))
		}
	}
	sb.WriteString("---\n\n")
	sb.WriteString(c.Content)
	return sb.String()
}

func formatSkillMdFromSkill(s *Skill) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", s.Name))
	sb.WriteString(fmt.Sprintf("description: %s\n", s.Description))
	if len(s.Tags) > 0 {
		sb.WriteString("tags:\n")
		for _, t := range s.Tags {
			sb.WriteString(fmt.Sprintf("  - %s\n", t))
		}
	}
	sb.WriteString("---\n\n")
	sb.WriteString(s.Content)
	return sb.String()
}

func sanitizeSkillName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	// Remove non-alphanumeric except underscore
	var clean strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			clean.WriteRune(r)
		}
	}
	return clean.String()
}
