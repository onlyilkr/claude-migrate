package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onlyilkr/claude-migrate/internal/guard"
	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/paths"
	"github.com/onlyilkr/claude-migrate/internal/report"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

type fakeGuard struct {
	running bool
	err     error
}

func (g fakeGuard) DesktopRunning() (bool, error) { return g.running, g.err }

type harness struct {
	*testutil.Home
	app      *App
	out, err *bytes.Buffer
}

func newHarness(t *testing.T, h *testutil.Home, stdin string, tty bool) *harness {
	t.Helper()
	var out, errb bytes.Buffer
	app := &App{
		In: strings.NewReader(stdin), Out: &out, Err: &errb, Home: h.Dir, IsTTY: tty, Guard: fakeGuard{},
		Now: func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) },
	}
	return &harness{Home: h, app: app, out: &out, err: &errb}
}

func (hs *harness) run(args ...string) int {
	hs.out.Reset()
	hs.err.Reset()
	return hs.app.Run(args)
}

// seed: old store with 3 sessions (s1 pinned), target store with 1 native session, signed in to target.
func seed(t *testing.T) *testutil.Home {
	t.Helper()
	h := testutil.NewHome(t)
	h.LoggedIn(testutil.AccNew, testutil.OrgNew, "dev@example.com", "Acme")
	for i, title := range []string{"First", "Second", "Third"} {
		id := fmt.Sprintf("s%d", i+1)
		cli := fmt.Sprintf("cli-%d", i+1)
		h.Record(testutil.AccOld, testutil.OrgOld, id, map[string]any{
			"sessionId": "local_" + id, "cliSessionId": cli, "title": title, "cwd": "/w/a",
			"isStarred": i == 0, "lastActivityAt": 1700000000000 + i,
		})
		h.Transcript("-w-a", cli)
	}
	h.Record(testutil.AccNew, testutil.OrgNew, "n1", map[string]any{"sessionId": "local_n1", "cliSessionId": "cli-native", "title": "Native"})
	return h
}

func targetDir(h *testutil.Home) string { return h.P.StoreDir(testutil.AccNew, testutil.OrgNew) }

func TestVersionAndHelp(t *testing.T) {
	hs := newHarness(t, testutil.NewHome(t), "", false)
	if code := hs.run("version"); code != exitOK || !strings.Contains(hs.out.String(), disclaimer) {
		t.Fatalf("version: %d %q", code, hs.out.String())
	}
	if code := hs.run("help"); code != exitOK || !strings.Contains(hs.out.String(), disclaimer) {
		t.Fatalf("help: %d", code)
	}
	if code := hs.run("frobnicate"); code != exitUsage {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestPlanIsDryRun(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	before := hs.Names(targetDir(hs.Home))
	if code := hs.run(); code != exitOK {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	out := hs.out.String()
	for _, want := range []string{disclaimer, "dev@example.com · Acme", "to copy 3 · already in target 0", "Dry run: nothing was written."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(hs.err.String(), "Source: a1a1a1a1/b1b1b1b1") {
		t.Errorf("auto-picked source not announced on stderr: %s", hs.err)
	}
	if after := hs.Names(targetDir(hs.Home)); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("plan wrote files: %v -> %v", before, after)
	}
}

func TestPlanJSON(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("plan", "--json"); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if pl.Counts.Copy != 3 || len(pl.Manual.Pins) != 1 {
		t.Fatalf("counts = %+v", pl.Counts)
	}
}

func TestApplyRefusedWhileDesktopRuns(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	hs.app.Guard = fakeGuard{running: true}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitDesktopRunning {
		t.Fatalf("exit %d", code)
	}
	if code := hs.run("undo"); code != exitDesktopRunning {
		t.Fatalf("undo exit %d", code)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 1 {
		t.Fatalf("target has %d files, want 1", n)
	}
}

func TestApplyIdempotentUndo(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply exit %d: %s", code, hs.err)
	}
	if !strings.Contains(hs.out.String(), "Copied 3 sessions.") || !strings.Contains(hs.out.String(), "claude-migrate undo") {
		t.Fatalf("apply output:\n%s", hs.out)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 4 {
		t.Fatalf("target has %d files, want 4", n)
	}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK || !strings.Contains(hs.out.String(), "Nothing to copy") {
		t.Fatalf("second apply: %d\n%s", code, hs.out)
	}
	if code := hs.run("undo"); code != exitOK {
		t.Fatalf("undo exit %d: %s", code, hs.err)
	}
	if got := hs.Names(targetDir(hs.Home)); len(got) != 1 || got[0] != "local_n1.json" {
		t.Fatalf("after undo: %v", got)
	}
	if code := hs.run("undo"); code != exitUsage {
		t.Fatalf("undo with no runs left: %d", code)
	}
}

func TestUndoKeepsModifiedUnlessForced(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	edited := targetDir(hs.Home) + "/local_s1.json"
	if err := os.WriteFile(edited, []byte(`{"renamed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := hs.run("undo"); code != exitError || !strings.Contains(hs.out.String(), "kept (changed since migration)") {
		t.Fatalf("undo: %d\n%s", code, hs.out)
	}
	if code := hs.run("undo", "--force"); code != exitOK {
		t.Fatalf("forced undo: %d %s", code, hs.err)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 1 {
		t.Fatalf("target has %d files after forced undo", n)
	}
}

func TestPins(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.out.String(), "not migrated yet") {
		t.Fatalf("pins before apply: %d\n%s", code, hs.out)
	}
	hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1")
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.out.String(), "- First  [pinned in target]") || strings.Contains(hs.out.String(), "Experimental") {
		t.Fatalf("pins after apply: %d\n%s", code, hs.out)
	}
	if code := hs.run("pins", "--json"); code != exitOK {
		t.Fatalf("pins --json: %d", code)
	}
	var rows []report.PinRow
	if err := json.Unmarshal(hs.out.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].SourceSessionID != "local_s1" || rows[0].TargetSessionID != "local_s1" {
		t.Fatalf("rows = %+v, err = %v\n%s", rows, err, hs.out)
	}
}

func TestExitCodes(t *testing.T) {
	t.Run("no account and no --to", func(t *testing.T) {
		h := seed(t)
		os.Remove(h.P.ClaudeJSON())
		if code := newHarness(t, h, "", false).run(); code != exitUsage {
			t.Fatalf("exit %d", code)
		}
	})
	t.Run("target store missing", func(t *testing.T) {
		h := seed(t)
		h.LoggedIn(testutil.AccNew, "b9b9b9b9-0000-4000-8000-000000000009", "dev@example.com", "Acme")
		// Two candidate sources exist once the target is elsewhere, so name one.
		if code := newHarness(t, h, "", false).run("plan", "--from", "a1a1a1a1/b1b1b1b1"); code != exitLayout {
			t.Fatalf("exit %d", code)
		}
	})
	t.Run("no sessions root", func(t *testing.T) {
		h := testutil.NewHome(t)
		h.LoggedIn(testutil.AccNew, testutil.OrgNew, "dev@example.com", "Acme")
		if code := newHarness(t, h, "", false).run(); code != exitLayout {
			t.Fatalf("exit %d", code)
		}
	})
	t.Run("same source and target", func(t *testing.T) {
		if code := newHarness(t, seed(t), "", false).run("plan", "--from", "a2a2a2a2/b2b2b2b2"); code != exitUsage {
			t.Fatalf("exit %d", code)
		}
	})
	t.Run("unknown flag", func(t *testing.T) {
		if code := newHarness(t, seed(t), "", false).run("plan", "--nope"); code != exitUsage {
			t.Fatalf("exit %d", code)
		}
	})
}

func TestSourceSelection(t *testing.T) {
	multi := func(t *testing.T) *testutil.Home {
		h := seed(t)
		h.Record(testutil.AccNew, testutil.OrgOld, "z", map[string]any{"cliSessionId": "cli-z", "title": "Other store", "lastActivityAt": 1800000000000})
		return h
	}
	if code := newHarness(t, multi(t), "", false).run(); code != exitUsage {
		t.Fatalf("non-TTY with several sources: exit %d", code)
	}
	hs := newHarness(t, multi(t), "2\n", true)
	if code := hs.run(); code != exitOK {
		t.Fatalf("TTY pick: exit %d: %s", code, hs.err)
	}
	if !strings.Contains(hs.err.String(), "[2] a1a1a1a1/b1b1b1b1") || !strings.Contains(hs.out.String(), "to copy 3") {
		t.Fatalf("picked wrong store:\nstderr %s\nstdout %s", hs.err, hs.out)
	}
	if code := newHarness(t, multi(t), "9\n", true).run(); code != exitUsage {
		t.Fatalf("invalid choice: exit %d", code)
	}
}

func TestDoctor(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	hs.app.Guard = fakeGuard{running: true}
	if code := hs.run("doctor"); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"✗ Claude Desktop is running", "✓ signed-in account: dev@example.com", "✗ cleanupPeriodDays", "2 session stores"} {
		if !strings.Contains(hs.out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, hs.out)
		}
	}
}

// Multibyte titles truncate on rune boundaries.
func TestTruncate(t *testing.T) {
	got := truncate(strings.Repeat("ğ", 50)+"🚀", 40)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 40 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate = %q", got)
	}
	if truncate("short", 40) != "short" {
		t.Fatal("short strings must pass through")
	}
}

// apply writes, so it never picks a source on its own.
func TestApplyRequiresFrom(t *testing.T) {
	hs := newHarness(t, seed(t), "1\n", true)
	if code := hs.run("apply"); code != exitUsage || !strings.Contains(hs.err.String(), "apply needs --from") {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 1 {
		t.Fatalf("target has %d files, want 1", n)
	}
}

// undo only ever deletes inside the <acct>/<org> store the run claims to target:
// a hand-edited manifest must not redirect it at another directory.
func TestUndoRefusesTargetDirOutsideStore(t *testing.T) {
	h := seed(t)
	hs := newHarness(t, h, "", false)
	elsewhere := t.TempDir()
	victim := h.File(elsewhere, "local_x.json", `{"keep":"me"}`)
	runs := hs.app.pathsFor(options{}).RunsDir()
	header := fmt.Sprintf(`{"tool_version":"t","source":"%s/%s","target":"%s/%s","target_dir":%q,"started_at":"2026-10-09T12:00:00Z"}`,
		testutil.AccOld, testutil.OrgOld, testutil.AccNew, testutil.OrgNew, elsewhere)
	entry := fmt.Sprintf(`{"path":%q,"sha256":%q}`, victim, manifest.HashHex([]byte(`{"keep":"me"}`)))
	mf := h.File(runs, "20261009T120000.000Z.jsonl", header+"\n"+entry+"\n")
	if code := hs.run("undo", "--force", mf); code != exitUsage {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the store was touched: %v", err)
	}
}

// A newer run that cannot be read might be the one the user means to undo, so
// undo without a path must not quietly fall back to an older run.
func TestUndoRefusesWhenNewestRunIsUnreadable(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	runs := hs.app.pathsFor(options{}).RunsDir()
	bad := hs.File(runs, "29990101T000000.000Z.jsonl", "")
	older := manifestsIn(t, hs)[0]
	if code := hs.run("undo"); code != exitUsage {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	if want := "the newest run(s) are unreadable: " + bad + "; pass a manifest path to undo an older run"; !strings.Contains(hs.err.String(), want) {
		t.Fatalf("stderr lacks %q:\n%s", want, hs.err)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 4 {
		t.Fatalf("undo removed files (%d left) although it refused", n)
	}
	if code := hs.run("undo", older); code != exitOK {
		t.Fatalf("undo with an explicit path: exit %d: %s", code, hs.err)
	}
}

func TestUndoWhenEveryRunIsUnreadable(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	runs := hs.app.pathsFor(options{}).RunsDir()
	hs.File(runs, "20261009T120000.000Z.jsonl", "")
	hs.File(runs, "20261009T130000.000Z.jsonl", "{not json\n")
	if code := hs.run("undo"); code != exitUsage {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	if want := "no readable migration runs (2 unreadable in " + runs + ")"; !strings.Contains(hs.err.String(), want) {
		t.Fatalf("stderr lacks %q:\n%s", want, hs.err)
	}
}

func TestMalformedAccountUUIDIsUsageError(t *testing.T) {
	h := seed(t)
	h.File(h.Dir, ".claude.json", `{"oauthAccount":{"accountUuid":"../..","organizationUuid":"`+testutil.OrgNew+`"}}`)
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan", "--from", "a1a1a1a1/b1b1b1b1"); code != exitUsage {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
}

func TestUnreadableStoreDoesNotAbort(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := seed(t)
	bad := h.Store("a3a3a3a3-0000-4000-8000-000000000003", "b3b3b3b3-0000-4000-8000-000000000003")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o700) })
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("plan exit %d: %s", code, hs.err)
	}
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.err.String(), "Source: a1a1a1a1/b1b1b1b1") {
		t.Fatalf("unreadable store must not be auto-picked: exit %d, stderr %s", code, hs.err)
	}
	if code := hs.run("doctor"); code != exitOK || !strings.Contains(hs.out.String(), "✗ a3a3a3a3/b3b3b3b3") ||
		!strings.Contains(hs.out.String(), "· unreadable: ") {
		t.Fatalf("doctor: exit %d\n%s", code, hs.out)
	}
}

func TestUndoJSON(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	if code := hs.run("undo", "--json"); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	if strings.Contains(hs.out.String(), "null") {
		t.Fatalf("arrays must be [] not null:\n%s", hs.out)
	}
	var got report.UndoJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if !got.Completed || len(got.Removed) != 3 || len(got.Missing)+len(got.Modified)+len(got.Rejected)+len(got.TempRemoved)+len(got.UnreadableNewer) != 0 ||
		!strings.HasSuffix(got.Manifest, ".jsonl") {
		t.Fatalf("undo json = %+v", got)
	}
}

func TestUndoJSONIncompleteStillFails(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	edited := targetDir(hs.Home) + "/local_s1.json"
	if err := os.WriteFile(edited, []byte(`{"renamed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := hs.run("undo", "--json"); code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	var got report.UndoJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if got.Completed || len(got.Modified) != 1 || len(got.Removed) != 2 {
		t.Fatalf("undo json = %+v", got)
	}
}

func TestDoctorJSON(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	hs.app.Guard = fakeGuard{running: true}
	if code := hs.run("doctor", "--json"); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	var got report.DoctorJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if !got.DesktopRunning || got.DesktopError != "" || got.CleanupPeriodSet || got.AccountError != "" || got.StoresError != "" {
		t.Fatalf("doctor json = %+v", got)
	}
	if got.Account == nil || got.Account.AccountUUID != testutil.AccNew || got.Account.OrgUUID != testutil.OrgNew ||
		got.Account.Email != "dev@example.com" || got.Account.OrgName != "Acme" {
		t.Fatalf("account = %+v", got.Account)
	}
	if len(got.Stores) != 2 {
		t.Fatalf("stores = %+v", got.Stores)
	}
	var old report.DoctorStore
	for _, s := range got.Stores {
		if s.ID == testutil.AccOld+"/"+testutil.OrgOld {
			old = s
		}
	}
	if old.Records != 3 || old.LastActivity == "" || len(old.SampleTitles) != 2 || old.Error != "" {
		t.Fatalf("old store = %+v", old)
	}
	if _, err := time.Parse(time.RFC3339, old.LastActivity); err != nil {
		t.Fatalf("last_activity %q: %v", old.LastActivity, err)
	}
}

func TestDoctorJSONErrors(t *testing.T) {
	h := testutil.NewHome(t) // no sessions root, no ~/.claude.json
	hs := newHarness(t, h, "", false)
	hs.app.Guard = fakeGuard{err: fmt.Errorf("boom")}
	if code := hs.run("doctor", "--json"); code != exitLayout {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(hs.out.String(), `"account": null`) {
		t.Fatalf("account must be null:\n%s", hs.out)
	}
	var got report.DoctorJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if got.DesktopRunning || !strings.Contains(got.DesktopError, "boom") || got.Account != nil || got.AccountError == "" ||
		got.StoresError == "" || got.Stores == nil || len(got.Stores) != 0 {
		t.Fatalf("doctor json = %+v", got)
	}
}

// Desktop only reads the real home's tree, so a --home elsewhere is not guarded.
func TestDesktopGuardSkippedOnlyForOtherHome(t *testing.T) {
	scratch := seed(t)
	hs := newHarness(t, scratch, "", false)
	hs.app.Home = t.TempDir() // the "real" home, unrelated to the scratch tree
	hs.app.Guard = fakeGuard{running: true}
	if code := hs.run("apply", "--home", scratch.Dir, "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply on another home: exit %d: %s", code, hs.err)
	}
	if n := len(hs.Names(targetDir(scratch))); n != 4 {
		t.Fatalf("scratch target has %d files, want 4", n)
	}
	if code := hs.run("undo", "--home", scratch.Dir); code != exitOK {
		t.Fatalf("undo on another home: exit %d: %s", code, hs.err)
	}
	if n := len(hs.Names(targetDir(scratch))); n != 1 {
		t.Fatalf("scratch target has %d files after undo, want 1", n)
	}
}

func TestDesktopGuardAppliesToRealHome(t *testing.T) {
	h := seed(t)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(h.Dir, link); err != nil {
		t.Fatal(err)
	}
	for name, flagHome := range map[string]string{"same path": h.Dir, "symlink to it": link} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, h, "", false)
			if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK { // a run for undo to find
				t.Fatalf("setup apply: %d %s", code, hs.err)
			}
			t.Cleanup(func() { hs.app.Guard = fakeGuard{}; hs.run("undo") })
			hs.app.Guard = fakeGuard{running: true}
			if code := hs.run("apply", "--home", flagHome, "--from", "a1a1a1a1/b1b1b1b1"); code != exitDesktopRunning {
				t.Fatalf("apply exit %d, want %d", code, exitDesktopRunning)
			}
			if code := hs.run("undo", "--home", flagHome); code != exitDesktopRunning {
				t.Fatalf("undo exit %d, want %d", code, exitDesktopRunning)
			}
		})
	}
}

// The guard decides by file identity, so other spellings of the real home, or a
// scratch home whose tree links into it, never write while Desktop runs.
func TestDesktopGuardRecognizesRealHomeSpellings(t *testing.T) {
	h := seed(t)
	claudeJSON, err := os.ReadFile(h.P.ClaudeJSON())
	if err != nil {
		t.Fatal(err)
	}
	scratchWith := func(t *testing.T, link func(scratch string)) string {
		s := t.TempDir()
		if err := os.WriteFile(filepath.Join(s, ".claude.json"), claudeJSON, 0o600); err != nil {
			t.Fatal(err)
		}
		link(s)
		return s
	}
	symlink := func(t *testing.T, target, at string) {
		if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(t *testing.T) string{
		"dot after chdir": func(t *testing.T) string { t.Chdir(h.Dir); return "." },
		"upper case": func(t *testing.T) string {
			u := strings.ToUpper(h.Dir)
			if _, err := os.Stat(u); err != nil {
				t.Skip("case-sensitive filesystem")
			}
			return u
		},
		"symlinked Claude dir": func(t *testing.T) string {
			return scratchWith(t, func(s string) {
				symlink(t, filepath.Join(h.Dir, "Library", "Application Support", "Claude"),
					filepath.Join(s, "Library", "Application Support", "Claude"))
			})
		},
		"symlinked store dirs": func(t *testing.T) string {
			return scratchWith(t, func(s string) {
				sp := paths.New(s)
				symlink(t, h.P.StoreDir(testutil.AccOld, testutil.OrgOld), sp.StoreDir(testutil.AccOld, testutil.OrgOld))
				symlink(t, targetDir(h), sp.StoreDir(testutil.AccNew, testutil.OrgNew))
			})
		},
	}
	for name, home := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, h, "", false)
			hs.app.Guard = fakeGuard{running: true}
			code := hs.run("apply", "--home", home(t), "--from", "a1a1a1a1/b1b1b1b1")
			if code == exitOK {
				t.Fatalf("apply succeeded while Desktop runs: %s", hs.out)
			}
			if n := len(hs.Names(targetDir(h))); n != 1 {
				t.Fatalf("real target has %d files, want 1 (exit %d)", n, code)
			}
		})
	}
	t.Run("undo with dot", func(t *testing.T) {
		hs := newHarness(t, h, "", false)
		if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
			t.Fatalf("setup apply: %d %s", code, hs.err)
		}
		hs.app.Guard = fakeGuard{running: true}
		t.Chdir(h.Dir)
		if code := hs.run("undo", "--home", "."); code != exitDesktopRunning {
			t.Fatalf("undo exit %d, want %d", code, exitDesktopRunning)
		}
		if n := len(hs.Names(targetDir(h))); n != 4 {
			t.Fatalf("undo removed files while Desktop runs: %d left", n)
		}
	})
}

func TestRemoteSessionRefused(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	hs.app.Guard = fakeGuard{err: guard.ErrRemoteSession}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitDesktopRunning {
		t.Fatalf("exit %d, want %d", code, exitDesktopRunning)
	}
}

// Copies keep the source ID, so the file names match and Desktop's global pin
// list still points at them.
func TestApplyKeepsSessionIDs(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	want := "local_n1.json,local_s1.json,local_s2.json,local_s3.json"
	if got := strings.Join(hs.Names(targetDir(hs.Home)), ","); got != want {
		t.Fatalf("target = %s, want %s", got, want)
	}
}

func TestNameConflictIsReportedAndSkipped(t *testing.T) {
	h := seed(t)
	other := h.Record(testutil.AccNew, testutil.OrgNew, "s2", map[string]any{"sessionId": "local_s2", "cliSessionId": "cli-theirs", "title": "Theirs"})
	before, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan", "--json"); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
		t.Fatal(err)
	}
	if pl.Counts.Copy != 2 || pl.Counts.NameConflicts != 1 || len(pl.NameConflicts) != 1 || pl.NameConflicts[0].File != "local_s2.json" {
		t.Fatalf("plan = %+v", pl.Counts)
	}
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.out.String(), "! file name already used in target by another session, skipped: \"Second\" (local_s2.json)") {
		t.Fatalf("text plan: %d\n%s", code, hs.out)
	}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply: %d %s", code, hs.err)
	}
	after, err := os.ReadFile(other)
	if err != nil || string(after) != string(before) {
		t.Fatalf("conflicting target file changed: %v\n%s", err, after)
	}
	if n := len(hs.Names(targetDir(h))); n != 4 { // n1, s2 (theirs), s1, s3
		t.Fatalf("target has %d files, want 4", n)
	}
}

const mismatchMsg = "~/.claude.json is signed in to account a2a2a2a2 but Claude Desktop last used account a1a1a1a1; pass --to <account>/<org> to choose the target explicitly"

// Desktop shows the account it last used, so a target that Claude Code alone
// points at may be the wrong store. apply refuses; plan and pins only warn.
func TestAccountMismatchRefusesApply(t *testing.T) {
	h := seed(t)
	h.DesktopLastAccount(testutil.AccOld)
	hs := newHarness(t, h, "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitUsage || !strings.Contains(hs.err.String(), mismatchMsg) {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	if n := len(hs.Names(targetDir(h))); n != 1 {
		t.Fatalf("target has %d files, want 1", n)
	}
	if _, err := os.Stat(hs.app.pathsFor(options{}).RunsDir()); err == nil {
		t.Fatal("a manifest was created")
	}
}

func TestAccountMismatchOnlyWarnsForPlanAndPins(t *testing.T) {
	h := seed(t)
	h.DesktopLastAccount(testutil.AccOld)
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan", "--json"); code != exitOK {
		t.Fatalf("plan exit %d: %s", code, hs.err)
	}
	if !strings.Contains(hs.err.String(), "warning: "+mismatchMsg) {
		t.Fatalf("stderr lacks the warning:\n%s", hs.err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
		t.Fatalf("stdout must stay one JSON document: %v\n%s", err, hs.out)
	}
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.err.String(), "warning: "+mismatchMsg) {
		t.Fatalf("pins exit %d, stderr %s", code, hs.err)
	}
}

func TestAccountMismatchIgnoredWithTo(t *testing.T) {
	h := seed(t)
	h.DesktopLastAccount(testutil.AccOld)
	hs := newHarness(t, h, "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1", "--to", "a2a2a2a2/b2b2b2b2"); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	if strings.Contains(hs.err.String(), "Claude Desktop last used") {
		t.Fatalf("--to must skip the cross-check: %s", hs.err)
	}
}

func TestNoAccountWarningWhenDesktopAgreesOrIsUnknown(t *testing.T) {
	agree := seed(t)
	agree.DesktopLastAccount(testutil.AccNew)
	for name, h := range map[string]*testutil.Home{"agrees": agree, "no config.json": seed(t)} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, h, "", false)
			if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
				t.Fatalf("exit %d: %s", code, hs.err)
			}
			if strings.Contains(hs.err.String(), "Claude Desktop last used") {
				t.Fatalf("unexpected warning: %s", hs.err)
			}
		})
	}
}

func TestDoctorComparesDesktopAccount(t *testing.T) {
	h := seed(t)
	h.DesktopLastAccount(testutil.AccOld)
	hs := newHarness(t, h, "", false)
	if code := hs.run("doctor"); code != exitOK || !strings.Contains(hs.out.String(), "✗ Claude Desktop last used account a1a1a1a1, but ~/.claude.json is signed in to a2a2a2a2") {
		t.Fatalf("exit %d\n%s", code, hs.out)
	}
	if code := hs.run("doctor", "--json"); code != exitOK {
		t.Fatal(hs.err)
	}
	var got report.DoctorJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil || got.DesktopLastAccount != testutil.AccOld || got.AccountsAgree {
		t.Fatalf("doctor json = %+v, err = %v", got, err)
	}

	h.DesktopLastAccount(testutil.AccNew)
	if code := hs.run("doctor"); code != exitOK || !strings.Contains(hs.out.String(), "✓ Claude Desktop last used the same account") {
		t.Fatalf("exit %d\n%s", code, hs.out)
	}
	hs.run("doctor", "--json")
	got = report.DoctorJSON{}
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil || got.DesktopLastAccount != testutil.AccNew || !got.AccountsAgree {
		t.Fatalf("doctor json = %+v, err = %v", got, err)
	}

	if err := os.Remove(h.P.DesktopConfigJSON()); err != nil {
		t.Fatal(err)
	}
	if code := hs.run("doctor"); code != exitOK || strings.Contains(hs.out.String(), "Claude Desktop last used") {
		t.Fatalf("line must be skipped without a lastKnownAccountUuid: exit %d\n%s", code, hs.out)
	}
	hs.run("doctor", "--json")
	got = report.DoctorJSON{}
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil || got.DesktopLastAccount != "" || !got.AccountsAgree {
		t.Fatalf("doctor json = %+v, err = %v", got, err)
	}
	if !strings.Contains(hs.out.String(), `"desktop_last_account": ""`) {
		t.Fatalf("desktop_last_account must be present and empty:\n%s", hs.out)
	}
}

// A record without a transcript opens empty in Desktop: skipped by default,
// copied with --include-orphans.
func TestIncludeOrphans(t *testing.T) {
	orphaned := func(t *testing.T) *harness {
		h := seed(t)
		h.Record(testutil.AccOld, testutil.OrgOld, "s4", map[string]any{"sessionId": "local_s4", "cliSessionId": "cli-4", "title": "Fourth"})
		return newHarness(t, h, "", false)
	}
	counts := func(t *testing.T, hs *harness, args ...string) report.Counts {
		t.Helper()
		if code := hs.run(append([]string{"plan", "--json"}, args...)...); code != exitOK {
			t.Fatalf("exit %d: %s", code, hs.err)
		}
		var pl report.PlanJSON
		if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
			t.Fatal(err)
		}
		return pl.Counts
	}
	hs := orphaned(t)
	if c := counts(t, hs); c.Copy != 3 || c.Orphans != 1 || c.MissingTranscript != 0 {
		t.Fatalf("default counts = %+v", c)
	}
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.out.String(), "no transcript (skipped) 1") ||
		!strings.Contains(hs.out.String(), "run with --include-orphans to copy them anyway (they open empty in Desktop)") {
		t.Fatalf("text plan: %d\n%s", code, hs.out)
	}
	if c := counts(t, hs, "--include-orphans"); c.Copy != 4 || c.Orphans != 0 || c.MissingTranscript != 1 {
		t.Fatalf("--include-orphans counts = %+v", c)
	}

	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 4 { // n1 + 3 with transcripts
		t.Fatalf("default apply: target has %d files, want 4", n)
	}
	hs.app.Now = func() time.Time { return time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC) } // another run, another manifest name
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1", "--include-orphans"); code != exitOK {
		t.Fatal(hs.err)
	}
	if n := len(hs.Names(targetDir(hs.Home))); n != 5 {
		t.Fatalf("apply --include-orphans: target has %d files, want 5", n)
	}
	if code := hs.run("undo", "--include-orphans"); code != exitUsage {
		t.Fatalf("undo must not accept --include-orphans: exit %d", code)
	}
}

func TestReapplySkipsSessionsDeletedInTarget(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply: %d %s", code, hs.err)
	}
	// Desktop deletes a session by removing its record and leaving a marker.
	dir := targetDir(hs.Home)
	if err := os.Remove(filepath.Join(dir, "local_s1.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deleted_s1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("re-apply: %d %s", code, hs.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "local_s1.json")); err == nil {
		t.Fatal("re-apply brought back a session deleted in the target")
	}
	if !strings.Contains(hs.out.String(), "deleted in target 1") {
		t.Fatalf("plan output does not report it:\n%s", hs.out)
	}
}

// With kept IDs a newer run can record the same path as an older one; undoing
// the older run must leave that file to the newer run and say so.
func TestUndoOfOlderRunKeepsFilesNewerRunOwns(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	run1 := manifestsIn(t, hs)[0]
	owned := filepath.Join(targetDir(hs.Home), "local_s1.json")
	if err := os.Remove(owned); err != nil {
		t.Fatal(err)
	}
	hs.app.Now = func() time.Time { return time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC) }
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK || !strings.Contains(hs.out.String(), "Copied 1 sessions.") {
		t.Fatalf("second apply: %d\n%s", code, hs.out)
	}

	if code := hs.run("undo", run1); code != exitOK {
		t.Fatalf("undo of the older run: %d %s", code, hs.err)
	}
	if want := "  ! kept (recorded again by a newer run): " + owned; !strings.Contains(hs.out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, hs.out)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Fatalf("the newer run's file was removed: %v", err)
	}
	if got := strings.Join(hs.Names(targetDir(hs.Home)), ","); got != "local_n1.json,local_s1.json" {
		t.Fatalf("target = %s", got)
	}

	if code := hs.run("undo"); code != exitOK {
		t.Fatalf("undo of the newest run: %d %s", code, hs.err)
	}
	if _, err := os.Stat(owned); err == nil {
		t.Fatal("undo of the newest run must remove the file")
	}
}

func TestUndoJSONReportsSuperseded(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	run1 := manifestsIn(t, hs)[0]
	owned := filepath.Join(targetDir(hs.Home), "local_s1.json")
	if err := os.Remove(owned); err != nil {
		t.Fatal(err)
	}
	hs.app.Now = func() time.Time { return time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC) }
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	if code := hs.run("undo", "--json", run1); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	var got report.UndoJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if len(got.Superseded) != 1 || got.Superseded[0] != owned || !got.Completed || len(got.Removed) != 2 {
		t.Fatalf("undo json = %+v", got)
	}
}

// manifestsIn lists the run files, oldest first.
func manifestsIn(t *testing.T, hs *harness) []string {
	t.Helper()
	runs := hs.app.pathsFor(options{}).RunsDir()
	var out []string
	for _, n := range hs.Names(runs) {
		out = append(out, filepath.Join(runs, n))
	}
	return out
}

// A symlink or directory named like a record occupies that name in the target:
// plan reports a name conflict and apply copies the rest instead of aborting.
func TestNonRegularTargetEntriesAreNameConflicts(t *testing.T) {
	h := seed(t)
	dir := targetDir(h)
	if err := os.Symlink(filepath.Join(dir, "local_n1.json"), filepath.Join(dir, "local_s1.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "local_s2.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan", "--json"); code != exitOK {
		t.Fatalf("plan exit %d: %s", code, hs.err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
		t.Fatal(err)
	}
	if pl.Counts.Copy != 1 || pl.Counts.NameConflicts != 2 || len(pl.NameConflicts) != 2 {
		t.Fatalf("plan = %+v", pl.Counts)
	}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply exit %d: %s", code, hs.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "local_s3.json")); err != nil {
		t.Fatalf("the other record was not copied: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "local_s2.json")); err != nil || !fi.IsDir() {
		t.Fatalf("the directory was touched: %v", err)
	}
}

// In a source store they are skipped as unreadable, never silently.
func TestNonRegularSourceEntriesAreUnreadable(t *testing.T) {
	h := seed(t)
	src := h.P.StoreDir(testutil.AccOld, testutil.OrgOld)
	if err := os.Mkdir(filepath.Join(src, "local_dirx.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.out.String(), "unreadable record, skipped: local_dirx.json") {
		t.Fatalf("plan: %d\n%s", code, hs.out)
	}
}

func TestNothingToCopyMessage(t *testing.T) {
	t.Run("target has every session", func(t *testing.T) {
		hs := newHarness(t, seed(t), "", false)
		if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
			t.Fatal(hs.err)
		}
		if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK ||
			!strings.Contains(hs.out.String(), "Nothing to copy: the target already has every session.\n") {
			t.Fatalf("exit %d\n%s", code, hs.out)
		}
	})
	t.Run("records skipped for other reasons", func(t *testing.T) {
		h := seed(t)
		for _, id := range []string{"s1", "s2", "s3"} {
			h.Record(testutil.AccNew, testutil.OrgNew, id, map[string]any{"sessionId": "local_" + id, "cliSessionId": "cli-theirs-" + id})
		}
		hs := newHarness(t, h, "", false)
		if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
			t.Fatalf("exit %d: %s", code, hs.err)
		}
		out := hs.out.String()
		if !strings.Contains(out, "Nothing to copy: 3 records were skipped; see the counts above.\n") || strings.Contains(out, "already has every session") {
			t.Fatalf("output:\n%s", out)
		}
	})
}

func planJSON(t *testing.T, hs *harness, args ...string) report.PlanJSON {
	t.Helper()
	if code := hs.run(append([]string{"plan", "--json"}, args...)...); code != exitOK {
		t.Fatalf("exit %d: %s", code, hs.err)
	}
	var pl report.PlanJSON
	if err := json.Unmarshal(hs.out.Bytes(), &pl); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	return pl
}

// Desktop's own pin list decides what is pinned: a session listed there counts
// even when its record says isStarred=false, and a starred record that is not in
// the list does not.
func TestPinsFollowDesktopPinList(t *testing.T) {
	h := seed(t) // s1 has isStarred=true, s2 and s3 do not
	h.DesktopPins("local_s2")
	hs := newHarness(t, h, "", false)

	pl := planJSON(t, hs)
	if len(pl.Manual.Pins) != 1 || pl.Manual.Pins[0].File != "local_s2.json" {
		t.Fatalf("pins = %+v", pl.Manual.Pins)
	}
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.out.String(), "- Second  [not migrated yet]") || strings.Contains(hs.out.String(), "First") {
		t.Fatalf("pins: %d\n%s", code, hs.out)
	}
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK || !strings.Contains(hs.out.String(), "1 pinned sessions: 1 are or will be pinned in the target") {
		t.Fatalf("apply: %d\n%s", code, hs.out)
	}
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.out.String(), "- Second  [pinned in target]") {
		t.Fatalf("pins after apply: %d\n%s", code, hs.out)
	}
}

func TestPinsFallBackToStarredWithoutDesktopPinList(t *testing.T) {
	h := seed(t)
	hs := newHarness(t, h, "", false)
	if pl := planJSON(t, hs); len(pl.Manual.Pins) != 1 || pl.Manual.Pins[0].File != "local_s1.json" {
		t.Fatalf("no config file: pins = %+v", pl.Manual.Pins)
	}
	h.File(filepath.Dir(h.P.SessionsRoot()), "claude_desktop_config.json", `{"preferences":{"epitaxyPrefs":{}}}`)
	if pl := planJSON(t, hs); len(pl.Manual.Pins) != 1 || pl.Manual.Pins[0].File != "local_s1.json" {
		t.Fatalf("no pin list: pins = %+v", pl.Manual.Pins)
	}
}

// A pinned session that an older version copied under another ID is in the
// target, but Desktop's pin names the source ID: the pin has to be redone.
func TestPinsToRedoForSessionsCopiedUnderAnotherID(t *testing.T) {
	h := seed(t)
	h.Record(testutil.AccNew, testutil.OrgNew, "other", map[string]any{"sessionId": "local_other", "cliSessionId": "cli-1", "title": "First"})
	h.DesktopPins("local_s1", "local_s2")
	hs := newHarness(t, h, "", false)

	pl := planJSON(t, hs)
	if len(pl.Manual.Pins) != 2 || len(pl.Manual.PinsToRedo) != 1 || pl.Manual.PinsToRedo[0].File != "local_s1.json" {
		t.Fatalf("manual = %+v", pl.Manual)
	}
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.out.String(),
		"2 pinned sessions: 1 are or will be pinned in the target; 1 were copied earlier under a different ID and are not pinned there — pin those again in Desktop (claude-migrate pins lists them)") {
		t.Fatalf("plan: %d\n%s", code, hs.out)
	}
	if code := hs.run("pins"); code != exitOK || !strings.Contains(hs.out.String(), "- First  [copied under a different ID; pin it again in Desktop]") {
		t.Fatalf("pins: %d\n%s", code, hs.out)
	}
}

// undoStuck applies a run, then puts a directory where the second record was, so
// undo removes the first file and fails reading the second.
func undoStuck(t *testing.T) *harness {
	t.Helper()
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatal(hs.err)
	}
	stuck := filepath.Join(targetDir(hs.Home), "local_s2.json")
	if err := os.Remove(stuck); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	return hs
}

// An error part-way through undo must not hide what was already removed.
func TestUndoPrintsPartialResultOnError(t *testing.T) {
	hs := undoStuck(t)
	if code := hs.run("undo"); code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	if !strings.Contains(hs.out.String(), "removed 1 ") || !strings.Contains(hs.err.String(), "error:") {
		t.Fatalf("stdout:\n%s\nstderr:\n%s", hs.out, hs.err)
	}
	if _, err := os.Stat(filepath.Join(targetDir(hs.Home), "local_s1.json")); err == nil {
		t.Fatal("local_s1.json should have been removed before the error")
	}
}

func TestUndoJSONPrintsPartialResultOnError(t *testing.T) {
	hs := undoStuck(t)
	if code := hs.run("undo", "--json"); code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	var got report.UndoJSON
	if err := json.Unmarshal(hs.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, hs.out)
	}
	if got.Completed || len(got.Removed) != 1 || !strings.HasSuffix(got.Removed[0], "local_s1.json") {
		t.Fatalf("undo json = %+v", got)
	}
	if !strings.Contains(hs.err.String(), "error:") {
		t.Fatalf("stderr lacks the error: %s", hs.err)
	}
}

// ~/.claude.json that cannot be read is a problem with the input, like one that
// does not parse: exit 2, not a generic error.
func TestUnreadableClaudeJSONIsUsageError(t *testing.T) {
	h := seed(t)
	if err := os.Remove(h.P.ClaudeJSON()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.P.ClaudeJSON(), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := newHarness(t, h, "", false).run("plan", "--from", "a1a1a1a1/b1b1b1b1"); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
}

func TestUndoOfMissingManifestIsUsageError(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	missing := filepath.Join(t.TempDir(), "nope.jsonl")
	if code := hs.run("undo", missing); code != exitUsage || !strings.Contains(hs.err.String(), "no such manifest: "+missing) {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
}

// An unreadable <account> directory is shown by doctor but never auto-picked,
// and does not stop plan from working on the other stores.
func TestUnreadableAccountDirDoesNotAbort(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := seed(t)
	bad := filepath.Join(h.P.SessionsRoot(), "a3a3a3a3-0000-4000-8000-000000000003")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o700) })
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan"); code != exitOK || !strings.Contains(hs.err.String(), "Source: a1a1a1a1/b1b1b1b1") {
		t.Fatalf("plan: exit %d, stderr %s", code, hs.err)
	}
	if code := hs.run("doctor"); code != exitOK || !strings.Contains(hs.out.String(), "✗ a3a3a3a3/") || !strings.Contains(hs.out.String(), "· unreadable: ") {
		t.Fatalf("doctor: exit %d\n%s", code, hs.out)
	}
}

func TestUndoSummaryCountsSupersededAndOmitsSkippedRuns(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("apply: %d %s", code, hs.err)
	}
	if err := os.Remove(filepath.Join(targetDir(hs.Home), "local_s1.json")); err != nil {
		t.Fatal(err)
	}
	hs.app.Now = func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) }
	if code := hs.run("apply", "--from", "a1a1a1a1/b1b1b1b1"); code != exitOK {
		t.Fatalf("second apply: %d %s", code, hs.err)
	}
	runs, _ := filepath.Glob(filepath.Join(hs.P.RunsDir(), "*.jsonl"))
	if len(runs) != 2 {
		t.Fatalf("runs = %v", runs)
	}
	if code := hs.run("undo", runs[0]); code != exitOK || !strings.Contains(hs.out.String(), "kept for a newer run 1") {
		t.Fatalf("undo older run: %d\n%s", code, hs.out)
	}
	if code := hs.run("undo", "--json"); code != exitOK {
		t.Fatalf("undo --json: %d %s", code, hs.err)
	}
	var doc map[string]any
	if err := json.Unmarshal(hs.out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["skipped_runs"]; ok {
		t.Fatal("undo JSON still carries the always-empty skipped_runs field")
	}
}

// --force cannot remove refused entries, so the message must not suggest it.
func TestUndoIncompleteMessageForRefusedEntries(t *testing.T) {
	h := seed(t)
	hs := newHarness(t, h, "", false)
	runs := h.P.RunsDir()
	if err := os.MkdirAll(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "local_x.json")
	hdr := fmt.Sprintf(`{"tool_version":"t","source":"%s/%s","target":"%s/%s","target_dir":%q,"started_at":"2026-10-09T12:00:00Z"}`,
		testutil.AccOld, testutil.OrgOld, testutil.AccNew, testutil.OrgNew, targetDir(h))
	m := filepath.Join(runs, "20261009T120000.000Z.jsonl")
	if err := os.WriteFile(m, []byte(hdr+"\n"+fmt.Sprintf(`{"path":%q,"sha256":"x"}`, outside)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := hs.run("undo", m); code != exitError {
		t.Fatalf("exit %d", code)
	}
	if msg := hs.err.String(); !strings.Contains(msg, "outside the target store") || strings.Contains(msg, "--force") {
		t.Fatalf("message = %q", msg)
	}
}

// The dry run's suggested command must keep apply's account cross-check
// active, so it names --to only when the user passed --to to plan.
func TestPlanSuggestsApplyWithoutTo(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run(); code != exitOK {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	if want := "To apply: claude-migrate apply --from a1a1a1a1/b1b1b1b1\n"; !strings.Contains(hs.out.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, hs.out)
	}
}

func TestPlanSuggestsApplyWithToWhenGiven(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("plan", "--to", "a2a2a2a2/b2b2b2b2"); code != exitOK {
		t.Fatalf("exit %d, stderr %s", code, hs.err)
	}
	if want := "To apply: claude-migrate apply --from a1a1a1a1/b1b1b1b1 --to a2a2a2a2/b2b2b2b2\n"; !strings.Contains(hs.out.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, hs.out)
	}
}

// Running exactly what plan printed must still hit the account cross-check.
func TestSuggestedApplyHitsAccountCrossCheck(t *testing.T) {
	h := seed(t)
	h.DesktopLastAccount(testutil.AccOld)
	hs := newHarness(t, h, "", false)
	if code := hs.run("plan"); code != exitOK {
		t.Fatalf("plan exit %d, stderr %s", code, hs.err)
	}
	var suggested []string
	for _, line := range strings.Split(hs.out.String(), "\n") {
		if cmd, ok := strings.CutPrefix(line, "To apply: claude-migrate "); ok {
			suggested = strings.Fields(cmd)
		}
	}
	if len(suggested) == 0 || suggested[0] != "apply" {
		t.Fatalf("no suggested apply command in:\n%s", hs.out)
	}
	if code := hs.run(suggested...); code != exitUsage || !strings.Contains(hs.err.String(), mismatchMsg) {
		t.Fatalf("%v: exit %d, stderr %s", suggested, code, hs.err)
	}
	if n := len(hs.Names(targetDir(h))); n != 1 {
		t.Fatalf("target has %d files, want 1", n)
	}
	if _, err := os.Stat(hs.app.pathsFor(options{}).RunsDir()); err == nil {
		t.Fatal("a manifest was created")
	}
}

// The suggested apply command must copy what the plan showed: --include-orphans
// is carried over when the plan used it.
func TestPlanSuggestionKeepsIncludeOrphans(t *testing.T) {
	hs := newHarness(t, seed(t), "", false)
	if code := hs.run("plan", "--include-orphans"); code != exitOK {
		t.Fatalf("plan: %d %s", code, hs.err)
	}
	if !strings.Contains(hs.out.String(), "To apply: claude-migrate apply --from a1a1a1a1/b1b1b1b1 --include-orphans\n") {
		t.Fatalf("suggestion lost --include-orphans:\n%s", hs.out)
	}
}
