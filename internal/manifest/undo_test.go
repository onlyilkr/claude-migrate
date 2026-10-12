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

type run struct {
	t      *testing.T
	target string
	runs   string
	w      *manifest.Writer
}

func newRun(t *testing.T) *run {
	t.Helper()
	r := &run{t: t, target: t.TempDir(), runs: filepath.Join(t.TempDir(), "runs")}
	w, err := manifest.Create(r.runs, manifest.Header{TargetDir: r.target, StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	r.w = w
	return r
}

// add records an entry for content and, if write is true, creates the file.
func (r *run) add(dir, name, content string, write bool) string {
	r.t.Helper()
	p := filepath.Join(dir, name)
	if err := r.w.Add(manifest.Entry{Path: p, SHA256: manifest.HashHex([]byte(content))}); err != nil {
		r.t.Fatal(err)
	}
	if write {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestUndo(t *testing.T) {
	r := newRun(t)
	keep := r.add(r.target, "local_keep.json", `{"a":1}`, true)
	gone := r.add(r.target, "local_gone.json", `{"b":1}`, false)
	mod := r.add(r.target, "local_mod.json", `{"c":1}`, true)
	if err := os.WriteFile(mod, []byte(`{"c":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(r.target, ".claude-migrate-local_z.tmp")
	native := filepath.Join(r.target, "local_native.json")
	for _, p := range []string{tmp, native} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.w.Close()

	res, err := manifest.Undo(r.w.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Removed, []string{keep}) || !slices.Equal(res.Missing, []string{gone}) ||
		!slices.Equal(res.Modified, []string{mod}) || !slices.Equal(res.TempRemoved, []string{tmp}) || res.Completed {
		t.Fatalf("first undo = %+v", res)
	}
	if !exists(mod) || !exists(native) || exists(keep) || exists(tmp) || !exists(r.w.Path) {
		t.Fatal("wrong files removed on first undo")
	}

	res, err = manifest.Undo(r.w.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Removed, []string{mod}) || !res.Completed {
		t.Fatalf("forced undo = %+v", res)
	}
	if exists(mod) || !exists(native) || exists(r.w.Path) || !exists(r.w.Path+".undone") {
		t.Fatal("forced undo must remove mod, keep native, rename manifest")
	}
	if _, _, err := manifest.Latest(r.runs); !errors.Is(err, manifest.ErrNoRuns) {
		t.Fatalf("undone run must not be latest: %v", err)
	}
}

func TestUndoRejectsPathsOutsideTarget(t *testing.T) {
	r := newRun(t)
	outside := r.add(t.TempDir(), "local_x.json", `{}`, true)
	notRecord := r.add(r.target, "notes.txt", `{}`, true)
	r.w.Close()

	res, err := manifest.Undo(r.w.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rejected) != 2 || res.Completed {
		t.Fatalf("res = %+v", res)
	}
	if !exists(outside) || !exists(notRecord) || !exists(r.w.Path) {
		t.Fatal("rejected files and the manifest must be left alone")
	}
}

func TestUndoRequiresTargetDir(t *testing.T) {
	runs := t.TempDir()
	w, err := manifest.Create(runs, manifest.Header{StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := manifest.Undo(w.Path, false); err == nil {
		t.Fatal("manifest without target_dir must be refused")
	}
}

func TestUndoSkipsRetractedEntries(t *testing.T) {
	r := newRun(t)
	foreign := r.add(r.target, "local_f.json", `{"ours":true}`, false)
	if err := os.WriteFile(foreign, []byte(`{"someone":"else"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.w.Add(manifest.Entry{Path: foreign, Retracted: true}); err != nil {
		t.Fatal(err)
	}
	r.w.Close()
	res, err := manifest.Undo(r.w.Path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(foreign) || len(res.Removed) != 0 || !res.Completed {
		t.Fatalf("undo --force touched a retracted path: %+v", res)
	}
}

// With kept IDs two runs can record the same path with the same hash. Undoing
// the older run must leave the file to the newer run that owns it now.
func TestUndoKeepsFilesANewerRunOwns(t *testing.T) {
	r1 := newRun(t)
	x := r1.add(r1.target, "local_x.json", `{"x":1}`, true)
	only1 := r1.add(r1.target, "local_only1.json", `{"y":1}`, true)
	r1.w.Close()

	// The file is removed and written again by a newer run with the same content.
	if err := os.Remove(x); err != nil {
		t.Fatal(err)
	}
	w2, err := manifest.Create(r1.runs, manifest.Header{TargetDir: r1.target, StartedAt: started.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	r2 := &run{t: t, target: r1.target, runs: r1.runs, w: w2}
	r2.add(r2.target, "local_x.json", `{"x":1}`, true)
	r2.w.Close()

	res, err := manifest.Undo(r1.w.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Superseded, []string{x}) || !slices.Equal(res.Removed, []string{only1}) || !res.Completed {
		t.Fatalf("undo of the older run = %+v", res)
	}
	if !exists(x) {
		t.Fatal("the older run's undo deleted a file the newer run owns")
	}
	if !exists(r1.w.Path + ".undone") {
		t.Fatal("superseded entries must not block completion")
	}

	res, err = manifest.Undo(r2.w.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Removed, []string{x}) || len(res.Superseded) != 0 || !res.Completed || exists(x) {
		t.Fatalf("undo of the newest run = %+v", res)
	}
}

// A newer run that retracted the path never owned it, and an unreadable or
// undone newer run cannot claim anything.
func TestUndoIgnoresNewerRunsThatDoNotOwnThePath(t *testing.T) {
	r1 := newRun(t)
	x := r1.add(r1.target, "local_x.json", `{"x":1}`, true)
	r1.w.Close()

	w2, err := manifest.Create(r1.runs, manifest.Header{TargetDir: r1.target, StartedAt: started.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	r2 := &run{t: t, target: r1.target, runs: r1.runs, w: w2}
	r2.add(r2.target, "local_x.json", `{"x":1}`, false)
	if err := r2.w.Add(manifest.Entry{Path: x, Retracted: true}); err != nil {
		t.Fatal(err)
	}
	r2.w.Close()
	if err := os.WriteFile(filepath.Join(r1.runs, "29990101T000000.000Z.jsonl"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	undone := filepath.Join(r1.runs, "29990102T000000.000Z.jsonl.undone")
	b, err := os.ReadFile(r2.w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(undone, b, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := manifest.Undo(r1.w.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Removed, []string{x}) || len(res.Superseded) != 0 || !res.Completed {
		t.Fatalf("undo = %+v", res)
	}
}

// An error part-way through still returns what was removed before it.
func TestUndoReturnsPartialResultOnError(t *testing.T) {
	r := newRun(t)
	first := r.add(r.target, "local_a.json", `{"a":1}`, true)
	stuck := r.add(r.target, "local_b.json", `{"b":1}`, false)
	r.add(r.target, "local_c.json", `{"c":1}`, true)
	if err := os.Mkdir(stuck, 0o700); err != nil { // reading a directory fails
		t.Fatal(err)
	}
	r.w.Close()

	res, err := manifest.Undo(r.w.Path, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !slices.Equal(res.Removed, []string{first}) || res.Completed || !exists(r.w.Path) {
		t.Fatalf("partial result = %+v", res)
	}
}

// A newer run whose body cannot be read claims nothing; undo says so.
func TestUndoReportsUnreadableNewerRuns(t *testing.T) {
	r := newRun(t)
	r.add(r.target, "local_k.json", `{"k":1}`, true)
	r.w.Close()
	newer := filepath.Join(r.runs, "20991231T000000.000Z.jsonl")
	hdr := `{"tool_version":"t","source":"a/b","target":"c/d","target_dir":"` + r.target + `","started_at":"2099-12-31T00:00:00Z"}`
	if err := os.WriteFile(newer, []byte(hdr+"\ngarbage\n{\"path\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := manifest.Undo(r.w.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.UnreadableNewer, []string{newer}) {
		t.Fatalf("UnreadableNewer = %v", res.UnreadableNewer)
	}
}
