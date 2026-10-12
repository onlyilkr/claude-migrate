package account_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/account"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

func TestCurrent(t *testing.T) {
	h := testutil.NewHome(t)
	h.LoggedIn(testutil.AccNew, testutil.OrgNew, "dev@example.com", "Acme")
	got, err := account.Current(h.P)
	if err != nil {
		t.Fatal(err)
	}
	want := account.Account{AccountUUID: testutil.AccNew, OrgUUID: testutil.OrgNew, Email: "dev@example.com", OrgName: "Acme"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCurrentErrors(t *testing.T) {
	cases := map[string]string{
		"missing file": "",
		"no oauth":     `{"theme":"dark"}`,
		"no org":       `{"oauthAccount":{"accountUuid":"x"}}`,
		"null oauth":   `{"oauthAccount":null}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewHome(t)
			if content != "" {
				h.File(h.Dir, ".claude.json", content)
			}
			if _, err := account.Current(h.P); !errors.Is(err, account.ErrNoAccount) {
				t.Fatalf("err = %v, want ErrNoAccount", err)
			}
		})
	}
}

func TestCurrentInvalidJSON(t *testing.T) {
	h := testutil.NewHome(t)
	h.File(h.Dir, ".claude.json", "{nope")
	_, err := account.Current(h.P)
	if !errors.Is(err, account.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// Anything but "not there" (here: a directory in its place; permission denied
// behaves the same) means the account cannot be determined: ErrInvalid, not a bare I/O error.
func TestCurrentUnreadableIsInvalid(t *testing.T) {
	h := testutil.NewHome(t)
	if err := os.Mkdir(h.P.ClaudeJSON(), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := account.Current(h.P)
	if !errors.Is(err, account.ErrInvalid) || errors.Is(err, account.ErrNoAccount) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// IDs become path components under the sessions root, so only UUIDs are accepted.
func TestCurrentRejectsNonUUIDIDs(t *testing.T) {
	cases := map[string]string{
		"traversal account": `{"oauthAccount":{"accountUuid":"../..","organizationUuid":"` + testutil.OrgNew + `"}}`,
		"traversal org":     `{"oauthAccount":{"accountUuid":"` + testutil.AccNew + `","organizationUuid":"../x"}}`,
		"uppercase account": `{"oauthAccount":{"accountUuid":"A2A2A2A2-0000-4000-8000-000000000002","organizationUuid":"` + testutil.OrgNew + `"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewHome(t)
			h.File(h.Dir, ".claude.json", content)
			_, err := account.Current(h.P)
			if !errors.Is(err, account.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
	h := testutil.NewHome(t)
	h.File(h.Dir, ".claude.json", cases["traversal org"])
	if _, err := account.Current(h.P); err == nil || !strings.Contains(err.Error(), "organizationUuid") {
		t.Fatalf("error should name the field: %v", err)
	}
}

func TestCleanupPeriodSet(t *testing.T) {
	h := testutil.NewHome(t)
	if account.CleanupPeriodSet(h.P) {
		t.Fatal("no settings file: want false")
	}
	h.File(h.Dir+"/.claude", "settings.json", `{"model":"x"}`)
	if account.CleanupPeriodSet(h.P) {
		t.Fatal("key absent: want false")
	}
	h.File(h.Dir+"/.claude", "settings.json", `{"cleanupPeriodDays":365}`)
	if !account.CleanupPeriodSet(h.P) {
		t.Fatal("key present: want true")
	}
}

func TestDesktopLastKnown(t *testing.T) {
	cases := map[string]string{
		"valid":          `{"lastKnownAccountUuid":"` + testutil.AccOld + `"}`,
		"absent":         `{"locale":"en-US"}`,
		"not a UUID":     `{"lastKnownAccountUuid":"../.."}`,
		"wrong type":     `{"lastKnownAccountUuid":42}`,
		"malformed JSON": `{"lastKnownAccountUuid":`,
		"missing file":   "",
	}
	want := map[string]string{"valid": testutil.AccOld}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewHome(t)
			if content != "" {
				h.File(filepath.Dir(h.P.SessionsRoot()), "config.json", content)
			}
			if got := account.DesktopLastKnown(h.P); got != want[name] {
				t.Fatalf("got %q, want %q", got, want[name])
			}
		})
	}
}

func TestDesktopPinnedIDs(t *testing.T) {
	h := testutil.NewHome(t)
	h.DesktopPins("local_a", "local_b")
	got, ok := account.DesktopPinnedIDs(h.P)
	if !ok || len(got) != 2 || !got["local_a"] || !got["local_b"] {
		t.Fatalf("got %v, %v", got, ok)
	}

	// An empty list is a real answer: nothing is pinned.
	h.DesktopPins()
	if got, ok := account.DesktopPinnedIDs(h.P); !ok || len(got) != 0 {
		t.Fatalf("empty list: got %v, %v", got, ok)
	}
}

func TestDesktopPinnedIDsUnavailable(t *testing.T) {
	cases := map[string]string{
		"missing file":     "",
		"not json":         "{nope",
		"no preferences":   `{"locale":"en-US"}`,
		"no epitaxyPrefs":  `{"preferences":{}}`,
		"no pin list":      `{"preferences":{"epitaxyPrefs":{}}}`,
		"pin list string":  `{"preferences":{"epitaxyPrefs":{"starred-local-code-sessions":"local_a"}}}`,
		"pin list object":  `{"preferences":{"epitaxyPrefs":{"starred-local-code-sessions":{"local_a":true}}}}`,
		"pin list null":    `{"preferences":{"epitaxyPrefs":{"starred-local-code-sessions":null}}}`,
		"preferences list": `{"preferences":[]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewHome(t)
			if content != "" {
				h.File(filepath.Dir(h.P.SessionsRoot()), "claude_desktop_config.json", content)
			}
			if got, ok := account.DesktopPinnedIDs(h.P); ok || got != nil {
				t.Fatalf("got %v, %v; want not ok", got, ok)
			}
		})
	}
}
