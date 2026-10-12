// Package testutil builds fake $HOME trees that mirror Claude Desktop's on-disk layout.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/paths"
)

// Distinct first 8 characters so short store IDs stay unambiguous in tests.
const (
	AccOld = "a1a1a1a1-0000-4000-8000-000000000001"
	OrgOld = "b1b1b1b1-0000-4000-8000-000000000001"
	AccNew = "a2a2a2a2-0000-4000-8000-000000000002"
	OrgNew = "b2b2b2b2-0000-4000-8000-000000000002"
)

type Home struct {
	t   testing.TB
	Dir string
	P   paths.Paths
}

func NewHome(t testing.TB) *Home {
	t.Helper()
	dir := t.TempDir()
	return &Home{t: t, Dir: dir, P: paths.New(dir)}
}

// Store creates (if needed) and returns the <account>/<org> store directory.
func (h *Home) Store(account, org string) string {
	h.t.Helper()
	dir := h.P.StoreDir(account, org)
	h.must(os.MkdirAll(dir, 0o700))
	return dir
}

// Record writes local_<id>.json with the given fields into the store and returns its path.
func (h *Home) Record(account, org, id string, fields map[string]any) string {
	h.t.Helper()
	b, err := json.MarshalIndent(fields, "", "  ")
	h.must(err)
	return h.File(h.Store(account, org), "local_"+id+".json", string(b))
}

// File writes content to dir/name, creating dir, and returns the path.
func (h *Home) File(dir, name, content string) string {
	h.t.Helper()
	h.must(os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, name)
	h.must(os.WriteFile(path, []byte(content), 0o600))
	return path
}

// Transcript creates ~/.claude/projects/<project>/<cliID>.jsonl.
func (h *Home) Transcript(project, cliID string) {
	h.t.Helper()
	h.File(filepath.Join(h.P.ProjectsDir(), project), cliID+".jsonl", "{}\n")
}

// LoggedIn writes ~/.claude.json with an oauthAccount block.
func (h *Home) LoggedIn(account, org, email, orgName string) {
	h.t.Helper()
	b, err := json.Marshal(map[string]any{"oauthAccount": map[string]any{
		"accountUuid": account, "organizationUuid": org,
		"emailAddress": email, "organizationName": orgName,
	}})
	h.must(err)
	h.File(h.Dir, ".claude.json", string(b))
}

// DesktopLastAccount writes Claude Desktop's config.json, which records the
// account Desktop last used.
func (h *Home) DesktopLastAccount(account string) {
	h.t.Helper()
	b, err := json.Marshal(map[string]any{"lastKnownAccountUuid": account, "locale": "en-US"})
	h.must(err)
	h.File(filepath.Dir(h.P.SessionsRoot()), "config.json", string(b))
}

// Names lists the entries of dir, sorted by name.
func (h *Home) Names(dir string) []string {
	h.t.Helper()
	entries, err := os.ReadDir(dir)
	h.must(err)
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (h *Home) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

// DesktopPins writes Claude Desktop's claude_desktop_config.json with the given
// session IDs ("local_<id>") as its global pin list.
func (h *Home) DesktopPins(ids ...string) {
	h.t.Helper()
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(map[string]any{"preferences": map[string]any{"epitaxyPrefs": map[string]any{
		"starred-local-code-sessions": ids,
		"dframe-local-slice":          map[string]any{"pinnedOrder": []string{}},
	}}})
	h.must(err)
	h.File(filepath.Dir(h.P.SessionsRoot()), "claude_desktop_config.json", string(b))
}
