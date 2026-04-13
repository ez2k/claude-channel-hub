package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MarketplaceSkill represents a skill from the skills.sh registry
type MarketplaceSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`  // e.g. "vercel-labs/agent-skills"
	Installs    int    `json:"installs,omitempty"`
	URL         string `json:"url,omitempty"`     // skills.sh page URL
	RepoURL     string `json:"repo_url,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// SearchResult holds marketplace search results
type SearchResult struct {
	Query    string             `json:"query"`
	Skills   []MarketplaceSkill `json:"skills"`
	Source   string             `json:"source"` // "skills.sh", "github"
	Total    int                `json:"total"`
}

// Marketplace handles searching and installing skills from external sources
type Marketplace struct {
	installDir string
	httpClient *http.Client
}

func NewMarketplace(installDir string) *Marketplace {
	return &Marketplace{
		installDir: installDir,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// GetInstallDir returns the installation directory
func (m *Marketplace) GetInstallDir() string {
	return m.installDir
}

// Search queries skills.sh API for available skills
func (m *Marketplace) Search(query string) (*SearchResult, error) {
	apiURL := fmt.Sprintf("https://skills.sh/api/search?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ClaudeHarness/3.0")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skills.sh API error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Parse response — skills.sh returns { "skills": [...] }
	var apiResp struct {
		Skills []struct {
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Source   string `json:"source"`
			Installs int    `json:"installs"`
			Desc     string `json:"description"`
		} `json:"skills"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		// Try alternative flat array format
		var skills []struct {
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Source   string `json:"source"`
			Installs int    `json:"installs"`
			Desc     string `json:"description"`
		}
		if err2 := json.Unmarshal(body, &skills); err2 != nil {
			return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, truncateStr(string(body), 200))
		}
		apiResp.Skills = skills
	}

	result := &SearchResult{
		Query:  query,
		Source: "skills.sh",
		Total:  len(apiResp.Skills),
	}

	for _, s := range apiResp.Skills {
		name := s.Name
		if name == "" {
			name = s.Slug
		}
		ms := MarketplaceSkill{
			Name:     name,
			Description: s.Desc,
			Source:   s.Source,
			Installs: s.Installs,
		}
		if s.Source != "" && name != "" {
			ms.URL = fmt.Sprintf("https://skills.sh/%s/%s", s.Source, name)
			ms.RepoURL = fmt.Sprintf("https://github.com/%s", s.Source)
		}
		result.Skills = append(result.Skills, ms)
	}

	return result, nil
}

// Install downloads a skill from GitHub and saves it locally
// source format: "owner/repo" or "owner/repo/skill-name"
func (m *Marketplace) Install(source, skillName string) error {
	// Parse source
	parts := strings.Split(source, "/")
	if len(parts) < 2 {
		return fmt.Errorf("invalid source format: use 'owner/repo' or 'owner/repo/skill-name'")
	}

	owner := parts[0]
	repo := parts[1]
	if skillName == "" && len(parts) >= 3 {
		skillName = parts[2]
	}
	if skillName == "" {
		return fmt.Errorf("skill name is required")
	}

	// Try multiple known paths for SKILL.md in GitHub repos
	paths := []string{
		fmt.Sprintf("%s/SKILL.md", skillName),
		fmt.Sprintf("skills/%s/SKILL.md", skillName),
		fmt.Sprintf("%s/skill.md", skillName),
	}

	var content string
	var found bool

	for _, path := range paths {
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", owner, repo, path)
		resp, err := m.httpClient.Get(rawURL)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			content = string(body)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("SKILL.md not found in %s/%s for skill '%s'", owner, repo, skillName)
	}

	// Save to local skills directory
	installPath := filepath.Join(m.installDir, sanitizeSkillName(skillName))
	if err := os.MkdirAll(installPath, 0755); err != nil {
		return fmt.Errorf("failed to create skill dir: %w", err)
	}

	skillFile := filepath.Join(installPath, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	// Also save metadata
	meta := map[string]interface{}{
		"source":       fmt.Sprintf("%s/%s", owner, repo),
		"skill":        skillName,
		"installed_at": time.Now().Format(time.RFC3339),
		"installed_from": "skills.sh",
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(installPath, ".metadata.json"), metaBytes, 0644)

	fmt.Printf("📦 Installed skill: %s from %s/%s\n", skillName, owner, repo)
	return nil
}

// InstallFromURL downloads a SKILL.md directly from a URL
func (m *Marketplace) InstallFromURL(skillName, skillURL string) error {
	resp, err := m.httpClient.Get(skillURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	installPath := filepath.Join(m.installDir, sanitizeSkillName(skillName))
	if err := os.MkdirAll(installPath, 0755); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(installPath, "SKILL.md"), body, 0644)
}

// ListInstalled returns all installed marketplace skills (with metadata)
func (m *Marketplace) ListInstalled() []map[string]string {
	var installed []map[string]string

	entries, err := os.ReadDir(m.installDir)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(m.installDir, e.Name(), ".metadata.json")
		info := map[string]string{"name": e.Name()}

		if data, err := os.ReadFile(metaPath); err == nil {
			var meta map[string]string
			if json.Unmarshal(data, &meta) == nil {
				for k, v := range meta {
					info[k] = v
				}
			}
		}
		installed = append(installed, info)
	}

	return installed
}

// Uninstall removes an installed skill
func (m *Marketplace) Uninstall(skillName string) error {
	path := filepath.Join(m.installDir, sanitizeSkillName(skillName))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("skill '%s' not found", skillName)
	}
	return os.RemoveAll(path)
}

// FormatSearchResults formats results for Telegram display
func FormatSearchResults(result *SearchResult) string {
	if result == nil || len(result.Skills) == 0 {
		return fmt.Sprintf("🔍 '%s' 검색 결과가 없습니다.", result.Query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 *'%s' 검색 결과* (%d건)\n\n", result.Query, result.Total))

	limit := 8
	if len(result.Skills) < limit {
		limit = len(result.Skills)
	}

	for i := 0; i < limit; i++ {
		s := result.Skills[i]
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		installs := ""
		if s.Installs > 0 {
			installs = fmt.Sprintf(" (%d installs)", s.Installs)
		}
		sb.WriteString(fmt.Sprintf("📦 *%s*%s\n", s.Name, installs))
		if desc != "" {
			sb.WriteString(fmt.Sprintf("   _%s_\n", desc))
		}
		if s.Source != "" {
			sb.WriteString(fmt.Sprintf("   설치: `/install %s/%s`\n", s.Source, s.Name))
		}
		sb.WriteString("\n")
	}

	if result.Total > limit {
		sb.WriteString(fmt.Sprintf("... 외 %d건 더", result.Total-limit))
	}

	return sb.String()
}

func truncateStr(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}
