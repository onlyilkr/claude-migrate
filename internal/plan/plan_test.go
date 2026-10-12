package plan_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/store"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

var (
	srcID = store.ID{Account: testutil.AccOld, Org: testutil.OrgOld}
	dstID = store.ID{Account: testutil.AccNew, Org: testutil.OrgNew}
)

func raw(keys ...string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for _, k := range keys {
		m[k] = json.RawMessage(`""`)
	}
	return m
}

func files(rs []store.Record) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.File)
	}
	return out
}

func TestBuild(t *testing.T) {
	scratch := filepath.Join("/scratch", testutil.AccOld, "org", "s1")
	src := &store.Store{
		ID: srcID, Dir: "/src",
		Records: []store.Record{
			{File: "local_nocli.json", Raw: raw("title")},
			{File: "local_del.json", CLISessionID: "c-del", Raw: raw("cliSessionId")},
			{File: "local_exists.json", CLISessionID: "c-exists", Starred: true, Raw: raw("cliSessionId")},
			{File: "local_ok.json", CLISessionID: "c-ok", Starred: true, Cwd: scratch, ScheduledTaskID: "t1",
				Raw: raw("cliSessionId", "title", "brandNewField")},
			{File: "local_ok2.json", CLISessionID: "c-ok", Raw: raw("cliSessionId")},
			{File: "local_notx.json", CLISessionID: "c-notx", Cwd: "/scratch/other-account/s", Raw: raw("cliSessionId")},
		},
		Deleted:        map[string]bool{"del": true},
		Unreadable:     []string{"local_bad.json"},
		ScheduledTasks: []string{"Daily"},
	}
	dst := &store.Store{ID: dstID, Dir: "/dst", Records: []store.Record{{File: "local_n.json", CLISessionID: "c-exists"}}}

	pl := plan.Build(plan.Input{
		Source: src, Target: dst,
		Transcripts: map[string]bool{"c-ok": true, "c-exists": true},
		ScratchRoot: "/scratch",
	})

	check := func(name string, got []store.Record, want ...string) {
		t.Helper()
		if !slices.Equal(files(got), want) {
			t.Errorf("%s = %v, want %v", name, files(got), want)
		}
	}
	check("Copy", pl.Copy, "local_ok.json")
	check("Orphans", pl.Orphans, "local_notx.json")
	check("AlreadyInTarget", pl.AlreadyInTarget, "local_exists.json")
	check("Deleted", pl.Deleted, "local_del.json")
	check("NoCLISessionID", pl.NoCLISessionID, "local_nocli.json")
	check("Duplicates", pl.Duplicates, "local_ok2.json")
	check("MissingTranscript", pl.MissingTranscript)
	check("Pins", pl.Manual.Pins, "local_exists.json", "local_ok.json")
	check("ScheduledTaskSessions", pl.Manual.ScheduledTaskSessions, "local_ok.json")
	check("ScratchSessions", pl.Manual.ScratchSessions, "local_ok.json")

	if !slices.Equal(pl.UnknownFields, []string{"brandNewField"}) {
		t.Errorf("UnknownFields = %v", pl.UnknownFields)
	}
	if !slices.Equal(pl.Unreadable, []string{"local_bad.json"}) || !slices.Equal(pl.Manual.ScheduledTasks, []string{"Daily"}) {
		t.Errorf("Unreadable = %v, ScheduledTasks = %v", pl.Unreadable, pl.Manual.ScheduledTasks)
	}
	if !pl.Manual.CleanupPeriodUnset || pl.TargetDir != "/dst" || pl.Source != srcID || pl.Target != dstID {
		t.Errorf("plan header wrong: %+v", pl)
	}
}

// Desktop replaces cliSessionId on resume and keeps the old one in
// priorCliSessionIds; a resumed session must still count as already migrated.
func TestBuildMatchesPriorCLISessionIDs(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_resumed_in_target.json", CLISessionID: "c-old", Raw: raw("cliSessionId")},
		{File: "local_resumed_in_source.json", CLISessionID: "c-new2", PriorCLISessionIDs: []string{"c-orig"}, Raw: raw("cliSessionId")},
		{File: "local_fresh.json", CLISessionID: "c-fresh", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst", Records: []store.Record{
		{File: "local_t1.json", CLISessionID: "c-new", PriorCLISessionIDs: []string{"c-old"}},
		{File: "local_t2.json", CLISessionID: "c-orig"},
	}}
	pl := plan.Build(plan.Input{Source: src, Target: dst, Transcripts: map[string]bool{"c-fresh": true}, ScratchRoot: "/scratch"})
	if got, want := files(pl.AlreadyInTarget), []string{"local_resumed_in_target.json", "local_resumed_in_source.json"}; !slices.Equal(got, want) {
		t.Fatalf("AlreadyInTarget = %v, want %v", got, want)
	}
	if got := files(pl.Copy); !slices.Equal(got, []string{"local_fresh.json"}) {
		t.Fatalf("Copy = %v", got)
	}
}

// A copy keeps its file name, so a source file whose name the target already
// uses for a different session must be skipped, never overwritten.
func TestBuildNameConflict(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_clash.json", CLISessionID: "c-src", Raw: raw("cliSessionId")},
		{File: "local_clash_bad.json", CLISessionID: "c-bad", Raw: raw("cliSessionId")},
		{File: "local_same.json", CLISessionID: "c-same", Raw: raw("cliSessionId")},
		{File: "local_free.json", CLISessionID: "c-free", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst",
		Records: []store.Record{
			{File: "local_clash.json", CLISessionID: "c-other"},
			{File: "local_same.json", CLISessionID: "c-same"},
		},
		Unreadable: []string{"local_clash_bad.json"},
	}
	pl := plan.Build(plan.Input{Source: src, Target: dst, Transcripts: map[string]bool{"c-src": true, "c-bad": true, "c-same": true, "c-free": true}, ScratchRoot: "/scratch"})
	if got, want := files(pl.NameConflicts), []string{"local_clash.json", "local_clash_bad.json"}; !slices.Equal(got, want) {
		t.Errorf("NameConflicts = %v, want %v", got, want)
	}
	if got := files(pl.AlreadyInTarget); !slices.Equal(got, []string{"local_same.json"}) {
		t.Errorf("AlreadyInTarget = %v: a session match wins over a name match", got)
	}
	if got := files(pl.Copy); !slices.Equal(got, []string{"local_free.json"}) {
		t.Errorf("Copy = %v", got)
	}
}

// A record whose transcript is not on disk opens empty in Desktop, so it is
// skipped unless IncludeOrphans asks for it; skipped orphans are not "live", so
// they add no manual follow-ups.
func TestBuildOrphans(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_a.json", CLISessionID: "c-orphan", Starred: true, ScheduledTaskID: "t1", Cwd: "/scratch/" + testutil.AccOld + "/org/s", Raw: raw("cliSessionId")},
		{File: "local_b.json", CLISessionID: "c-orphan", Raw: raw("cliSessionId")},
		{File: "local_c.json", CLISessionID: "c-ok", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst"}
	in := plan.Input{Source: src, Target: dst, Transcripts: map[string]bool{"c-ok": true}, ScratchRoot: "/scratch"}

	pl := plan.Build(in)
	if got := files(pl.Copy); !slices.Equal(got, []string{"local_c.json"}) {
		t.Errorf("Copy = %v", got)
	}
	if got := files(pl.Orphans); !slices.Equal(got, []string{"local_a.json"}) {
		t.Errorf("Orphans = %v", got)
	}
	if got := files(pl.Duplicates); !slices.Equal(got, []string{"local_b.json"}) {
		t.Errorf("Duplicates = %v: the second record of an orphan is a duplicate, not a second orphan", got)
	}
	if len(pl.MissingTranscript) != 0 || len(pl.Manual.Pins) != 0 || len(pl.Manual.ScratchSessions) != 0 || len(pl.Manual.ScheduledTaskSessions) != 0 {
		t.Errorf("skipped orphans must not feed reports: %+v", pl)
	}

	in.IncludeOrphans = true
	pl = plan.Build(in)
	if got := files(pl.Copy); !slices.Equal(got, []string{"local_a.json", "local_c.json"}) {
		t.Errorf("Copy with IncludeOrphans = %v", got)
	}
	if got := files(pl.MissingTranscript); !slices.Equal(got, []string{"local_a.json"}) || len(pl.Orphans) != 0 {
		t.Errorf("MissingTranscript = %v, Orphans = %v", got, files(pl.Orphans))
	}
	if len(pl.Manual.Pins) != 1 || len(pl.Manual.ScheduledTaskSessions) != 1 {
		t.Errorf("copied orphans are live: %+v", pl.Manual)
	}
}

// Kept IDs make a target deleted_<id> marker name the same session: the user
// removed it from the target account, so a re-run must not bring it back.
func TestBuildSkipsSessionsDeletedInTarget(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_gone.json", CLISessionID: "c-gone", Starred: true, Raw: raw("cliSessionId")},
		{File: "local_keep.json", CLISessionID: "c-keep", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst", Deleted: map[string]bool{"gone": true}}
	pl := plan.Build(plan.Input{Source: src, Target: dst, Transcripts: map[string]bool{"c-gone": true, "c-keep": true}, ScratchRoot: "/scratch"})
	if got := files(pl.DeletedInTarget); !slices.Equal(got, []string{"local_gone.json"}) {
		t.Fatalf("DeletedInTarget = %v", got)
	}
	if got := files(pl.Copy); !slices.Equal(got, []string{"local_keep.json"}) {
		t.Fatalf("Copy = %v", got)
	}
	if len(pl.Manual.Pins) != 0 {
		t.Fatalf("a session deleted in the target is not live: pins = %v", files(pl.Manual.Pins))
	}
}

// Desktop's own pin list decides what is pinned; isStarred is only the fallback
// for when that list cannot be read.
func TestBuildPinsFollowDesktopPinList(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_listed.json", SessionID: "local_listed", CLISessionID: "c-listed", Raw: raw("cliSessionId")},
		{File: "local_starred.json", SessionID: "local_starred", CLISessionID: "c-starred", Starred: true, Raw: raw("cliSessionId")},
		{File: "local_same.json", SessionID: "local_same", CLISessionID: "c-same", Raw: raw("cliSessionId")},
		{File: "local_moved.json", SessionID: "local_moved", CLISessionID: "c-moved", Raw: raw("cliSessionId")},
		{File: "local_unlisted_moved.json", SessionID: "local_unlisted_moved", CLISessionID: "c-um", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst", Records: []store.Record{
		{File: "local_same.json", CLISessionID: "c-same"},
		{File: "local_renamed.json", CLISessionID: "c-moved"},
		{File: "local_renamed2.json", CLISessionID: "c-um"},
	}}
	tx := map[string]bool{"c-listed": true, "c-starred": true, "c-same": true, "c-moved": true, "c-um": true}
	in := plan.Input{Source: src, Target: dst, Transcripts: tx, ScratchRoot: "/scratch",
		Pinned: map[string]bool{"local_listed": true, "local_same": true, "local_moved": true}}

	pl := plan.Build(in)
	if got, want := files(pl.Manual.Pins), []string{"local_listed.json", "local_same.json", "local_moved.json"}; !slices.Equal(got, want) {
		t.Errorf("Pins = %v, want %v (listed counts even with isStarred false; starred but unlisted does not)", got, want)
	}
	if got, want := files(pl.Manual.PinsToRedo), []string{"local_moved.json"}; !slices.Equal(got, want) {
		t.Errorf("PinsToRedo = %v, want %v", got, want)
	}

	in.Pinned = map[string]bool{}
	if pl := plan.Build(in); len(pl.Manual.Pins) != 0 || len(pl.Manual.PinsToRedo) != 0 {
		t.Errorf("an empty pin list means nothing is pinned: %+v", pl.Manual)
	}

	in.Pinned = nil
	pl = plan.Build(in)
	if got, want := files(pl.Manual.Pins), []string{"local_starred.json"}; !slices.Equal(got, want) {
		t.Errorf("fallback Pins = %v, want %v", got, want)
	}
	if len(pl.Manual.PinsToRedo) != 0 {
		t.Errorf("fallback PinsToRedo = %v", files(pl.Manual.PinsToRedo))
	}
}

// A copy made earlier under another ID that the user already pinned again in
// the target needs no second re-pin.
func TestPinsAlreadyRepinnedInTargetAreNotRedone(t *testing.T) {
	src := &store.Store{ID: srcID, Dir: "/src", Records: []store.Record{
		{File: "local_a.json", SessionID: "local_a", CLISessionID: "c-a", Raw: raw("cliSessionId")},
		{File: "local_b.json", SessionID: "local_b", CLISessionID: "c-b", Raw: raw("cliSessionId")},
	}}
	dst := &store.Store{ID: dstID, Dir: "/dst", Records: []store.Record{
		{File: "local_x.json", SessionID: "local_x", CLISessionID: "c-a"},
		{File: "local_y.json", SessionID: "local_y", CLISessionID: "c-b"},
	}}
	pinned := map[string]bool{"local_a": true, "local_b": true, "local_x": true}
	pl := plan.Build(plan.Input{Source: src, Target: dst, Transcripts: map[string]bool{"c-a": true, "c-b": true}, ScratchRoot: "/scratch", Pinned: pinned})
	if got := files(pl.Manual.Pins); !slices.Equal(got, []string{"local_a.json", "local_b.json"}) {
		t.Fatalf("Pins = %v", got)
	}
	if got := files(pl.Manual.PinsToRedo); !slices.Equal(got, []string{"local_b.json"}) {
		t.Fatalf("PinsToRedo = %v", got)
	}
}
