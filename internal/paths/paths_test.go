package paths

import "testing"

func TestPaths(t *testing.T) {
	p := New("/h")
	cases := []struct{ got, want string }{
		{p.SessionsRoot(), "/h/Library/Application Support/Claude/claude-code-sessions"},
		{p.ScratchRoot(), "/h/Library/Application Support/Claude/scratch-workspaces"},
		{p.DesktopConfigJSON(), "/h/Library/Application Support/Claude/config.json"},
		{p.DesktopPrefsJSON(), "/h/Library/Application Support/Claude/claude_desktop_config.json"},
		{p.ProjectsDir(), "/h/.claude/projects"},
		{p.ClaudeJSON(), "/h/.claude.json"},
		{p.SettingsJSON(), "/h/.claude/settings.json"},
		{p.RunsDir(), "/h/.claude-migrate/runs"},
		{p.StoreDir("acc", "org"), "/h/Library/Application Support/Claude/claude-code-sessions/acc/org"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
