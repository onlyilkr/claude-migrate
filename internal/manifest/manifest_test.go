package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/manifest"
)

var started = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestWriteRead(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	h := manifest.Header{ToolVersion: "test", Source: "a/b", Target: "c/d", TargetDir: "/t", StartedAt: started}
	w, err := manifest.Create(runs, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []manifest.Entry{
		{Path: "/t/local_1.json", SourceFile: "local_x.json", CLISessionID: "c1", SHA256: "aa"},
		{Path: "/t/local_2.json", SourceFile: "local_y.json", CLISessionID: "c2", SHA256: "bb"},
	} {
		if err := w.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(w.Path) != "20261009T120000.000Z.jsonl" {
		t.Fatalf("name = %s", filepath.Base(w.Path))
	}
	gh, entries, err := manifest.Read(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if gh.Source != "a/b" || gh.Target != "c/d" || gh.TargetDir != "/t" || gh.ToolVersion != "test" || !gh.StartedAt.Equal(started) {
		t.Fatalf("header = %+v", gh)
	}
	if len(entries) != 2 || entries[1].CLISessionID != "c2" || entries[1].SHA256 != "bb" {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := manifest.Create(runs, h); err == nil {
		t.Fatal("second Create with same timestamp must fail, not overwrite")
	}
}

func writeRaw(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const header = `{"tool_version":"t","source":"a/b","target":"c/d","target_dir":"/t","started_at":"2026-10-09T12:00:00Z"}` + "\n"

func TestReadIgnoresTruncatedLastLine(t *testing.T) {
	p := writeRaw(t, header+`{"path":"/t/local_1.json","sha256":"aa"}`+"\n"+`{"path":"/t/lo`)
	_, entries, err := manifest.Read(p)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
}

func TestReadRejectsCorruptMiddleLine(t *testing.T) {
	p := writeRaw(t, header+"garbage\n"+`{"path":"/t/local_1.json","sha256":"aa"}`+"\n")
	if _, _, err := manifest.Read(p); err == nil {
		t.Fatal("corrupt middle line must be an error")
	}
}

func TestReadRejectsMissingHeader(t *testing.T) {
	if _, _, err := manifest.Read(writeRaw(t, "")); err == nil {
		t.Fatal("empty manifest must be an error")
	}
}

func writeRun(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLatest(t *testing.T) {
	runs := t.TempDir()
	if _, _, err := manifest.Latest(runs); !errors.Is(err, manifest.ErrNoRuns) {
		t.Fatalf("empty: err = %v", err)
	}
	for _, n := range []string{"20261009T120000.000Z.jsonl", "20261010T080000.000Z.jsonl", "20261011T080000.000Z.jsonl.undone"} {
		writeRun(t, runs, n, header)
	}
	got, skipped, err := manifest.Latest(runs)
	if err != nil || filepath.Base(got) != "20261010T080000.000Z.jsonl" || len(skipped) != 0 {
		t.Fatalf("latest = %s, skipped %v, %v", got, skipped, err)
	}
}

func TestLatestSkipsUnreadableHeaders(t *testing.T) {
	runs := t.TempDir()
	good := writeRun(t, runs, "20261009T120000.000Z.jsonl", header)
	empty := writeRun(t, runs, "20261010T080000.000Z.jsonl", "")
	junk := writeRun(t, runs, "20261011T080000.000Z.jsonl", "not json\n"+header)
	got, skipped, err := manifest.Latest(runs)
	if err != nil || got != good {
		t.Fatalf("latest = %s, %v; want %s", got, err, good)
	}
	if want := []string{junk, empty}; !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v (newest first)", skipped, want)
	}
}

func TestLatestAllUnreadable(t *testing.T) {
	runs := t.TempDir()
	bad := writeRun(t, runs, "20261009T120000.000Z.jsonl", "")
	_, skipped, err := manifest.Latest(runs)
	if !errors.Is(err, manifest.ErrNoRuns) || !slices.Equal(skipped, []string{bad}) {
		t.Fatalf("skipped = %v, err = %v", skipped, err)
	}
}

// A readable header with a damaged body is still the latest run: undo must
// surface the corruption instead of silently falling back to an older run.
func TestLatestKeepsCorruptBodyRun(t *testing.T) {
	runs := t.TempDir()
	writeRun(t, runs, "20261009T120000.000Z.jsonl", header)
	bad := writeRun(t, runs, "20261010T080000.000Z.jsonl", header+"garbage\n"+`{"path":"/t/local_1.json","sha256":"aa"}`+"\n")
	got, skipped, err := manifest.Latest(runs)
	if err != nil || got != bad || len(skipped) != 0 {
		t.Fatalf("latest = %s, skipped %v, %v", got, skipped, err)
	}
	if _, _, err := manifest.Read(got); err == nil {
		t.Fatal("corrupt body must still error when read")
	}
}

func TestHashHex(t *testing.T) {
	if manifest.HashHex([]byte("abc")) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("sha256 mismatch")
	}
}

// A header that parses but carries no target says nothing about where the run
// wrote: `{}` and `null` are as unreadable as garbage.
func TestLatestSkipsHeadersWithoutTarget(t *testing.T) {
	runs := t.TempDir()
	good := writeRun(t, runs, "20261009T120000.000Z.jsonl", header)
	noTarget := writeRun(t, runs, "20261010T080000.000Z.jsonl", `{"tool_version":"t","target_dir":"/t"}`+"\n")
	noDir := writeRun(t, runs, "20261011T080000.000Z.jsonl", `{"tool_version":"t","target":"c/d"}`+"\n")
	empty := writeRun(t, runs, "20261012T080000.000Z.jsonl", "{}\n")
	null := writeRun(t, runs, "20261013T080000.000Z.jsonl", "null\n")
	got, skipped, err := manifest.Latest(runs)
	if err != nil || got != good {
		t.Fatalf("latest = %s, %v; want %s", got, err, good)
	}
	if want := []string{null, empty, noDir, noTarget}; !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
}
