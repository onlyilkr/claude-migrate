package migrate_test

import (
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

// installFixture copies testdata/realistic into the fake home as the old store.
func installFixture(t *testing.T, h *testutil.Home) {
	t.Helper()
	const src = "testdata/realistic"
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := h.Store(testutil.AccOld, testutil.OrgOld)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "{{SCRATCH}}", h.P.ScratchRoot())
		s = strings.ReplaceAll(s, "{{SRC_ACCOUNT}}", testutil.AccOld)
		h.File(dst, e.Name(), s)
	}
}

// Expected numbers were measured on the real store on 2026-10-09.
func TestRealisticFixture(t *testing.T) {
	h := testutil.NewHome(t)
	installFixture(t, h)
	h.Store(testutil.AccNew, testutil.OrgNew)
	src, err := store.Load(h.P, oldID)
	if err != nil {
		t.Fatal(err)
	}
	tgt, err := store.Load(h.P, newID)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture ships no transcripts: by default every copyable record is an orphan.
	in := plan.Input{Source: src, Target: tgt, Transcripts: map[string]bool{}, ScratchRoot: h.P.ScratchRoot()}
	if skipped := plan.Build(in); len(skipped.Copy) != 0 || len(skipped.Orphans) != 79 {
		t.Fatalf("without IncludeOrphans: copy %d, orphans %d; want 0 and 79", len(skipped.Copy), len(skipped.Orphans))
	}
	in.IncludeOrphans = true
	pl := plan.Build(in)

	got := map[string]int{
		"records": len(src.Records), "unreadable": len(pl.Unreadable), "copy": len(pl.Copy),
		"noCLI": len(pl.NoCLISessionID), "deleted": len(pl.Deleted), "duplicates": len(pl.Duplicates),
		"pins": len(pl.Manual.Pins), "scheduledTaskSessions": len(pl.Manual.ScheduledTaskSessions),
		"scheduledTasks": len(pl.Manual.ScheduledTasks), "scratch": len(pl.Manual.ScratchSessions),
		"unknownFields": len(pl.UnknownFields), "missingTranscript": len(pl.MissingTranscript), "orphans": len(pl.Orphans),
	}
	want := map[string]int{
		"records": 80, "unreadable": 0, "copy": 79, "noCLI": 1, "deleted": 0, "duplicates": 0,
		"pins": 25, "scheduledTaskSessions": 2, "scheduledTasks": 4, "scratch": 5, "unknownFields": 0,
		"missingTranscript": 79, "orphans": 0,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %d, want %d", k, got[k], w)
		}
	}

	w, err := manifest.Create(h.P.RunsDir(), manifest.Header{TargetDir: pl.TargetDir, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	n, err := migrate.Apply(pl, w)
	w.Close()
	if err != nil || n != 79 {
		t.Fatalf("apply: n = %d, err = %v", n, err)
	}
	after, err := store.Load(h.P, newID)
	if err != nil {
		t.Fatal(err)
	}
	bySrc := map[string]store.Record{}
	for _, r := range src.Records {
		bySrc[r.CLISessionID] = r
	}
	for _, r := range after.Records {
		assertPreserved(t, bySrc[r.CLISessionID].Raw, r.Raw)
	}
	again := plan.Build(plan.Input{Source: src, Target: after, ScratchRoot: h.P.ScratchRoot(), IncludeOrphans: true})
	if len(again.Copy) != 0 || len(again.AlreadyInTarget) != 79 {
		t.Fatalf("second plan: copy %d, already %d", len(again.Copy), len(again.AlreadyInTarget))
	}
}
