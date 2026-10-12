// Package paths resolves the macOS locations claude-migrate reads and writes.
package paths

import (
	"path/filepath"
	"regexp"
)

// Paths roots every location at Home so tests can use a fake home directory.
type Paths struct{ Home string }

func New(home string) Paths { return Paths{Home: home} }

func (p Paths) claudeSupport() string {
	return filepath.Join(p.Home, "Library", "Application Support", "Claude")
}

func (p Paths) SessionsRoot() string { return filepath.Join(p.claudeSupport(), "claude-code-sessions") }
func (p Paths) ScratchRoot() string  { return filepath.Join(p.claudeSupport(), "scratch-workspaces") }

// DesktopConfigJSON is Claude Desktop's own config; it records the account Desktop last used.
func (p Paths) DesktopConfigJSON() string { return filepath.Join(p.claudeSupport(), "config.json") }

// DesktopPrefsJSON is Claude Desktop's preferences file; it holds the global pin list.
func (p Paths) DesktopPrefsJSON() string {
	return filepath.Join(p.claudeSupport(), "claude_desktop_config.json")
}

func (p Paths) ProjectsDir() string  { return filepath.Join(p.Home, ".claude", "projects") }
func (p Paths) ClaudeJSON() string   { return filepath.Join(p.Home, ".claude.json") }
func (p Paths) SettingsJSON() string { return filepath.Join(p.Home, ".claude", "settings.json") }
func (p Paths) RunsDir() string      { return filepath.Join(p.Home, ".claude-migrate", "runs") }

// StoreDir is the directory Desktop keeps one account×org's session records in.
func (p Paths) StoreDir(account, org string) string {
	return filepath.Join(p.SessionsRoot(), account, org)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether s is a lowercase UUID, the only shape of account and
// org ID that is safe to use as a path component under the sessions root.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }
