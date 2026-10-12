package migrate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/migrate"
	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/store"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

var (
	oldID = store.ID{Account: testutil.AccOld, Org: testutil.OrgOld}
	newID = store.ID{Account: testutil.AccNew, Org: testutil.OrgNew}
)

var changed = map[string]bool{"bridgeSessionIds": true, "scheduledTaskId": true}

func compact(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// assertPreserved checks every field outside the change list is JSON-identical.
func assertPreserved(t *testing.T, src, out map[string]json.RawMessage) {
	t.Helper()
	for k, v := range src {
		if changed[k] {
			continue
		}
		got, ok := out[k]
		if !ok {
			t.Errorf("field %q dropped", k)
			continue
		}
		if compact(t, v) != compact(t, got) {
			t.Errorf("field %q changed:\n src %s\n out %s", k, v, got)
		}
	}
	for k := range out {
		if _, ok := src[k]; !ok && !changed[k] {
			t.Errorf("unexpected new field %q", k)
		}
	}
}

func TestTransform(t *testing.T) {
	src := map[string]json.RawMessage{
		"sessionId":              json.RawMessage(`"local_old"`),
		"cliSessionId":           json.RawMessage(`"c1"`),
		"bridgeSessionIds":       json.RawMessage(`["session_01X"]`),
		"scheduledTaskId":        json.RawMessage(`"t1"`),
		"remoteMcpServersConfig": json.RawMessage(`[{"uuid":"u","name":"Docs"}]`),
		"toolSurfaceSnapshot":    json.RawMessage(`{"cliVersion":"2.1"}`),
		"title":                  json.RawMessage(`"<b>&</b> ğüş 🚀"`),
	}
	out := migrate.Transform(src)
	if string(out["sessionId"]) != `"local_old"` || string(out["bridgeSessionIds"]) != `[]` {
		t.Fatalf("sessionId/bridge = %s / %s", out["sessionId"], out["bridgeSessionIds"])
	}
	if _, ok := out["scheduledTaskId"]; ok {
		t.Fatal("scheduledTaskId must be removed")
	}
	assertPreserved(t, src, out)
	if string(src["bridgeSessionIds"]) != `["session_01X"]` {
		t.Fatal("Transform must not mutate its input")
	}
	bare := migrate.Transform(map[string]json.RawMessage{"cliSessionId": json.RawMessage(`"c"`)})
	if string(bare["bridgeSessionIds"]) != `[]` {
		t.Fatal("bridgeSessionIds must be set even when absent")
	}
}

func TestEncodeKeepsHTMLAndUnicode(t *testing.T) {
	data, err := migrate.Encode(map[string]json.RawMessage{"title": json.RawMessage(`"<b>&</b> ğüş 🚀"`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"<b>&</b> ğüş 🚀"`) {
		t.Fatalf("encoded = %s", data)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := migrate.WriteAtomic(dir, "local_x.json", []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := migrate.WriteAtomic(dir, "local_x.json", []byte(`{"other":1}`)); err == nil {
		t.Fatal("must refuse to overwrite")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "local_x.json"))
	entries, _ := os.ReadDir(dir)
	if string(b) != "{}\n" || len(entries) != 1 {
		t.Fatalf("content %q, %d entries (tmp file left behind?)", b, len(entries))
	}
	fi, _ := os.Stat(filepath.Join(dir, "local_x.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func seed(t *testing.T) *testutil.Home {
	h := testutil.NewHome(t)
	h.Record(testutil.AccOld, testutil.OrgOld, "a", map[string]any{
		"sessionId": "local_a", "cliSessionId": "c-a", "title": "A",
		"bridgeSessionIds": []string{"session_1"}, "remoteMcpServersConfig": []any{map[string]any{"uuid": "u1"}},
	})
	h.Record(testutil.AccOld, testutil.OrgOld, "b", map[string]any{"sessionId": "local_b", "cliSessionId": "c-b", "scheduledTaskId": "t1"})
	h.Store(testutil.AccNew, testutil.OrgNew)
	return h
}

func build(t *testing.T, h *testutil.Home) (plan.Plan, *store.Store) {
	t.Helper()
	src, err := store.Load(h.P, oldID)
	if err != nil {
		t.Fatal(err)
	}
	tgt, err := store.Load(h.P, newID)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Build(plan.Input{Source: src, Target: tgt, ScratchRoot: h.P.ScratchRoot(), IncludeOrphans: true}), src
}

func create(t *testing.T, h *testutil.Home, pl plan.Plan) *manifest.Writer {
	t.Helper()
	w, err := manifest.Create(h.P.RunsDir(), manifest.Header{TargetDir: pl.TargetDir, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestApply(t *testing.T) {
	h := seed(t)
	pl, src := build(t, h)
	w := create(t, h, pl)
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}

	tgt, err := store.Load(h.P, newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgt.Records) != 2 {
		t.Fatalf("target records = %+v", tgt.Records)
	}
	// The ID is kept: same file name, same sessionId, so Desktop's global pin
	// list (keyed by session ID) still matches the copy.
	for i, r := range tgt.Records {
		if r.File != src.Records[i].File || r.SessionID != src.Records[i].SessionID {
			t.Fatalf("copy %d is %s (%s), want source name %s (%s)", i, r.File, r.SessionID, src.Records[i].File, src.Records[i].SessionID)
		}
		if string(r.Raw["sessionId"]) != string(src.Records[i].Raw["sessionId"]) {
			t.Fatalf("sessionId bytes changed: %s -> %s", src.Records[i].Raw["sessionId"], r.Raw["sessionId"])
		}
	}
	bySrc := map[string]store.Record{}
	for _, r := range src.Records {
		bySrc[r.CLISessionID] = r
	}
	for _, r := range tgt.Records {
		assertPreserved(t, bySrc[r.CLISessionID].Raw, r.Raw)
	}

	_, entries, err := manifest.Read(w.Path)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(e.Path)
		if err != nil || manifest.HashHex(b) != e.SHA256 {
			t.Fatalf("manifest hash does not match %s", e.Path)
		}
	}

	again, _ := build(t, h)
	if len(again.Copy) != 0 || len(again.AlreadyInTarget) != 2 {
		t.Fatalf("second plan must be a no-op: copy %d, already %d", len(again.Copy), len(again.AlreadyInTarget))
	}
}

// A different session already owns the file name in the target: it is skipped
// and the existing file is left byte for byte as it was.
func TestApplySkipsNameConflict(t *testing.T) {
	h := seed(t)
	existing := h.Record(testutil.AccNew, testutil.OrgNew, "a", map[string]any{"sessionId": "local_a", "cliSessionId": "c-other", "title": "Theirs"})
	before, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := build(t, h)
	if len(pl.NameConflicts) != 1 || len(pl.Copy) != 1 {
		t.Fatalf("conflicts %d, copy %d; want 1 and 1", len(pl.NameConflicts), len(pl.Copy))
	}
	w := create(t, h, pl)
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err != nil || n != 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	after, err := os.ReadFile(existing)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing target file changed: %v\n%s", err, after)
	}
	if got := h.Names(pl.TargetDir); len(got) != 2 || got[0] != "local_a.json" || got[1] != "local_b.json" {
		t.Fatalf("target = %v", got)
	}
}

// If a same-named file appears between plan and write, the write is refused
// before the manifest records it, so undo --force can never delete a file this
// run did not create.
func TestApplyRefusesLateNameConflictWithoutRecordingIt(t *testing.T) {
	h := seed(t)
	pl, _ := build(t, h)
	foreign := h.Record(testutil.AccNew, testutil.OrgNew, "b", map[string]any{"cliSessionId": "c-late"})
	w := create(t, h, pl)
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err == nil || n != 1 {
		t.Fatalf("n = %d, err = %v; want 1 and an overwrite refusal", n, err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file gone: %v", err)
	}
	res, err := manifest.Undo(w.Path, true)
	if err != nil || len(res.Removed) != 1 || !res.Completed {
		t.Fatalf("undo = %+v, err = %v", res, err)
	}
	if got := h.Names(pl.TargetDir); len(got) != 1 || got[0] != "local_b.json" {
		t.Fatalf("target after forced undo = %v; the foreign file must survive", got)
	}
}

// A temp file left by a crash must not block the next apply.
func TestWriteAtomicIgnoresStaleTemp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".claude-migrate-local_x.tmp"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrate.WriteAtomic(dir, "local_x.json", []byte("{}\n")); err != nil {
		t.Fatalf("stale temp blocked the write: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "local_x.json")); string(b) != "{}\n" {
		t.Fatalf("content = %q", b)
	}
}

// When a write fails after its manifest entry, the entry is retracted so undo
// can never treat whatever ends up at that path as ours.
func TestApplyRetractsEntryWhenWriteFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := seed(t)
	pl, _ := build(t, h)
	w := create(t, h, pl)
	if err := os.Chmod(pl.TargetDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(pl.TargetDir, 0o700) })
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err == nil || n != 0 {
		t.Fatalf("n = %d, err = %v; want a write failure", n, err)
	}
	_, entries, err := manifest.Read(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Retracted || !entries[1].Retracted || entries[1].Path != entries[0].Path {
		t.Fatalf("entries = %+v; want the entry followed by its retraction", entries)
	}
}

// Once link(2) has published a record it is ours, even if the directory fsync
// then fails: it counts as written and its entry is not retracted.
func TestApplyKeepsPublishedRecordWhenDirSyncFails(t *testing.T) {
	h := seed(t)
	pl, _ := build(t, h)
	w := create(t, h, pl)
	defer migrate.SetSyncDir(func(string) error { return errors.New("fsync failed") })()
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err == nil || n != 1 {
		t.Fatalf("n = %d, err = %v; want 1 written and the fsync error", n, err)
	}
	_, entries, err := manifest.Read(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Retracted {
		t.Fatalf("entries = %+v; want one unretracted entry", entries)
	}
	if _, err := os.Stat(entries[0].Path); err != nil {
		t.Fatalf("published record missing: %v", err)
	}
}
