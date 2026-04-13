package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded skill definition (mirrors Claude Code's skill system)
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Command     string   // Telegram command (e.g., /code, /doc)
	Content     string   // Full SKILL.md content (injected into system prompt)
	Dir         string   // Skill directory path
	Tags        []string `yaml:"tags"`
}

// Registry holds all loaded skills
type Registry struct {
	skills map[string]*Skill
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]*Skill)}
}

// LoadFromDir scans a directory for skill folders containing SKILL.md
// Structure mirrors Claude Code:
//
//	skills/
//	  code/SKILL.md
//	  doc/SKILL.md
//	  search/SKILL.md
func (r *Registry) LoadFromDir(baseDir string) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("failed to read skills dir %s: %w", baseDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(baseDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); os.IsNotExist(err) {
			continue
		}

		skill, err := loadSkill(skillPath, entry.Name())
		if err != nil {
			fmt.Printf("⚠️  Failed to load skill %s: %v\n", entry.Name(), err)
			continue
		}

		r.skills[skill.Name] = skill
		fmt.Printf("✅ Loaded skill: /%s — %s\n", skill.Command, skill.Description)
	}

	return nil
}

// loadSkill parses a SKILL.md file with YAML front matter
func loadSkill(path, dirName string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	skill := &Skill{
		Name:    dirName,
		Command: dirName,
		Dir:     filepath.Dir(path),
		Content: content,
	}

	// Parse YAML front matter (between --- delimiters)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			if err := yaml.Unmarshal([]byte(parts[1]), skill); err == nil {
				skill.Content = strings.TrimSpace(parts[2])
			}
			// Generate command from name if not set
			if skill.Command == dirName && skill.Name != dirName {
				skill.Command = sanitizeCommand(skill.Name)
			}
		}
	}

	if skill.Description == "" {
		skill.Description = fmt.Sprintf("Activate the %s skill", skill.Name)
	}

	return skill, nil
}

func sanitizeCommand(name string) string {
	cmd := strings.ToLower(name)
	cmd = strings.ReplaceAll(cmd, " ", "_")
	cmd = strings.ReplaceAll(cmd, "-", "_")
	return cmd
}

// Get returns a skill by name or command
func (r *Registry) Get(nameOrCmd string) (*Skill, bool) {
	nameOrCmd = strings.TrimPrefix(nameOrCmd, "/")
	// Direct lookup
	if s, ok := r.skills[nameOrCmd]; ok {
		return s, true
	}
	// Search by command
	for _, s := range r.skills {
		if s.Command == nameOrCmd {
			return s, true
		}
	}
	return nil, false
}

// List returns all skills
func (r *Registry) List() []*Skill {
	result := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		result = append(result, s)
	}
	return result
}

// BuildSystemPrompt generates a system prompt section listing available skills
func (r *Registry) BuildSystemPrompt() string {
	if len(r.skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n<available_skills>\n")
	for _, s := range r.skills {
		sb.WriteString(fmt.Sprintf("- /%s: %s\n", s.Command, s.Description))
	}
	sb.WriteString("</available_skills>\n")
	sb.WriteString("When a skill is activated, its full instructions will be provided in the conversation context.\n")
	return sb.String()
}
