// Package account reads the locally signed-in Claude Code account and user settings.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/onlyilkr/claude-migrate/internal/paths"
)

var ErrNoAccount = errors.New("no signed-in account in ~/.claude.json (sign in to Claude Code or pass --to)")

// ErrInvalid means ~/.claude.json cannot be read (other than being absent), does not parse, or holds IDs that are not UUIDs.
var ErrInvalid = errors.New("invalid ~/.claude.json")

type Account struct {
	AccountUUID string `json:"accountUuid"`
	OrgUUID     string `json:"organizationUuid"`
	Email       string `json:"emailAddress"`
	OrgName     string `json:"organizationName"`
}

// Current returns the account and organization Claude Code is signed in to.
func Current(p paths.Paths) (Account, error) {
	b, err := os.ReadFile(p.ClaudeJSON())
	if errors.Is(err, fs.ErrNotExist) {
		return Account{}, ErrNoAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var doc struct {
		OAuth *Account `json:"oauthAccount"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Account{}, fmt.Errorf("%w: parse %s: %v", ErrInvalid, p.ClaudeJSON(), err)
	}
	if doc.OAuth == nil || doc.OAuth.AccountUUID == "" || doc.OAuth.OrgUUID == "" {
		return Account{}, ErrNoAccount
	}
	// The IDs become directory names under the sessions root; accept nothing but UUIDs.
	if !paths.IsUUID(doc.OAuth.AccountUUID) {
		return Account{}, fmt.Errorf("%w: oauthAccount.accountUuid is not a UUID", ErrInvalid)
	}
	if !paths.IsUUID(doc.OAuth.OrgUUID) {
		return Account{}, fmt.Errorf("%w: oauthAccount.organizationUuid is not a UUID", ErrInvalid)
	}
	return *doc.OAuth, nil
}

// CleanupPeriodSet reports whether ~/.claude/settings.json sets cleanupPeriodDays.
func CleanupPeriodSet(p paths.Paths) bool {
	b, err := os.ReadFile(p.SettingsJSON())
	if err != nil {
		return false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	_, ok := doc["cleanupPeriodDays"]
	return ok
}

// DesktopLastKnown returns the account UUID Claude Desktop last used
// (lastKnownAccountUuid in its config.json), or "" when that file is missing,
// unreadable, or holds no valid UUID. It is a cross-check, never a requirement.
func DesktopLastKnown(p paths.Paths) string {
	b, err := os.ReadFile(p.DesktopConfigJSON())
	if err != nil {
		return ""
	}
	var doc struct {
		Last string `json:"lastKnownAccountUuid"`
	}
	if json.Unmarshal(b, &doc) != nil || !paths.IsUUID(doc.Last) {
		return ""
	}
	return doc.Last
}

// DesktopPinnedIDs returns the session IDs ("local_<id>") in Claude Desktop's
// global pin list: preferences.epitaxyPrefs["starred-local-code-sessions"] in
// claude_desktop_config.json. It only reads. ok is false when the file is
// missing, unreadable, or the list is absent or not an array; an empty list is
// a real answer (ok, nothing pinned). Entries that are not strings are ignored.
func DesktopPinnedIDs(p paths.Paths) (ids map[string]bool, ok bool) {
	b, err := os.ReadFile(p.DesktopPrefsJSON())
	if err != nil {
		return nil, false
	}
	var doc struct {
		Preferences struct {
			EpitaxyPrefs struct {
				Starred []json.RawMessage `json:"starred-local-code-sessions"`
			} `json:"epitaxyPrefs"`
		} `json:"preferences"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Preferences.EpitaxyPrefs.Starred == nil {
		return nil, false
	}
	ids = map[string]bool{}
	for _, raw := range doc.Preferences.EpitaxyPrefs.Starred {
		var id string
		if json.Unmarshal(raw, &id) == nil && id != "" {
			ids[id] = true
		}
	}
	return ids, true
}
