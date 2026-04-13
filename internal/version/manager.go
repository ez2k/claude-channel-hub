package version

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Manager handles Claude Code version installation and selection.
type Manager struct {
	versionsDir    string
	defaultVersion string
	mu             sync.RWMutex
}

// NewManager creates a version manager.
// versionsDir is where versions are installed (e.g., ~/.claude-channel-hub/versions/).
// defaultVersion is the fallback version ("latest" or a specific version like "2.1.104").
func NewManager(versionsDir, defaultVersion string) *Manager {
	if versionsDir == "" {
		home, _ := os.UserHomeDir()
		versionsDir = filepath.Join(home, ".claude-channel-hub", "versions")
	}
	if defaultVersion == "" {
		defaultVersion = "latest"
	}
	return &Manager{
		versionsDir:    versionsDir,
		defaultVersion: defaultVersion,
	}
}

// Resolve returns the path to the claude binary for the given version.
// If version is empty, uses the default version.
// If version is "system", returns just "claude" (use system PATH).
// If version is "latest", returns the system claude binary.
func (m *Manager) Resolve(version string) string {
	if version == "" {
		version = m.defaultVersion
	}
	// For "latest" or "system", use the system-installed binary
	if version == "latest" || version == "system" {
		return "claude"
	}
	// Check if specific version is installed locally
	binPath := filepath.Join(m.versionsDir, version, "node_modules", ".bin", "claude")
	if _, err := os.Stat(binPath); err == nil {
		return binPath
	}
	// Fall back to system
	log.Printf("⚠️  Version %s not installed locally, falling back to system claude", version)
	return "claude"
}

// Install downloads and installs a specific Claude Code version.
func (m *Manager) Install(version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := filepath.Join(m.versionsDir, version)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create version dir: %w", err)
	}

	// Initialize package.json if needed
	pkgJSON := filepath.Join(dir, "package.json")
	if _, err := os.Stat(pkgJSON); os.IsNotExist(err) {
		if err := os.WriteFile(pkgJSON, []byte(`{"private":true}`), 0644); err != nil {
			return fmt.Errorf("write package.json: %w", err)
		}
	}

	// npm install @anthropic-ai/claude-code@version
	pkg := fmt.Sprintf("@anthropic-ai/claude-code@%s", version)
	cmd := exec.Command("npm", "install", pkg)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("📦 Installing Claude Code %s...", version)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install %s: %w", pkg, err)
	}

	log.Printf("✅ Claude Code %s installed at %s", version, dir)
	return nil
}

// List returns all installed versions.
func (m *Manager) List() []VersionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var versions []VersionInfo

	// System version
	if sysVer := systemVersion(); sysVer != "" {
		versions = append(versions, VersionInfo{
			Version: sysVer,
			Path:    "claude",
			System:  true,
			Active:  m.defaultVersion == "latest" || m.defaultVersion == "system",
		})
	}

	// Locally installed versions
	entries, err := os.ReadDir(m.versionsDir)
	if err != nil {
		return versions
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ver := entry.Name()
		binPath := filepath.Join(m.versionsDir, ver, "node_modules", ".bin", "claude")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		versions = append(versions, VersionInfo{
			Version: ver,
			Path:    binPath,
			System:  false,
			Active:  m.defaultVersion == ver,
		})
	}

	return versions
}

// Uninstall removes a locally installed version.
func (m *Manager) Uninstall(version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := filepath.Join(m.versionsDir, version)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("version %s not installed", version)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}

	log.Printf("🗑️  Claude Code %s uninstalled", version)
	return nil
}

// SetDefault changes the default version.
func (m *Manager) SetDefault(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultVersion = version
	log.Printf("🔄 Default Claude Code version set to %s", version)
}

// DefaultVersion returns the current default version string.
func (m *Manager) DefaultVersion() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultVersion
}

// SystemVersion returns the version string of the system claude binary.
func (m *Manager) SystemVersion() string {
	return systemVersion()
}

// systemVersion returns the version of the system-installed claude binary.
func systemVersion() string {
	cmd := exec.Command("claude", "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// VersionInfo describes an installed Claude Code version.
type VersionInfo struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	System  bool   `json:"system"`
	Active  bool   `json:"active"`
}
