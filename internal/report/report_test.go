package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/report"
	"github.com/onlyilkr/claude-migrate/internal/store"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

var (
	srcID  = store.ID{Account: testutil.AccOld, Org: testutil.OrgOld}
	dstID  = store.ID{Account: testutil.AccNew, Org: testutil.OrgNew}
	pinned = store.Record{File: "local_p.json", SessionID: "local_p", CLISessionID: "c-p", Title: "Pinned one", Starred: true}
)

func samplePlan() plan.Plan {
	return plan.Plan{
		Source: srcID, Target: dstID, TargetDir: "/dst",
		Copy:              []store.Record{pinned, {File: "local_q.json", CLISessionID: "c-q"}},
		MissingTranscript: []store.Record{{File: "local_q.json", CLISessionID: "c-q"}},
		NoCLISessionID:    []store.Record{{File: "local_n.json"}},
		NameConflicts:     []store.Record{{File: "local_c.json", Title: "Clash"}},
		Orphans:           []store.Record{{File: "local_o1.json"}, {File: "local_o2.json"}},
		UnknownFields:     []string{"brandNewField"},
		Manual: plan.Manual{
			Pins:               []store.Record{pinned},
			ScheduledTasks:     []string{"Daily check"},
			ScratchSessions:    []store.Record{pinned},
			CleanupPeriodUnset: true,
		},
	}
}

func TestText(t *testing.T) {
	var buf bytes.Buffer
	report.Text(&buf, samplePlan())
	out := buf.String()
	for _, want := range []string{
		"Plan: a1a1a1a1/b1b1b1b1 -> a2a2a2a2/b2b2b2b2",
		"to copy 2 · already in target 0 · deleted 0 · deleted in target 0 · no cliSessionId 1 · duplicates 0 · name conflicts 1 · no transcript (skipped) 2 · transcript missing 1 · unreadable 0",
		"fields changed on copy: bridgeSessionIds (emptied), scheduledTaskId (removed)",
		"! file name already used in target by another session, skipped: \"Clash\" (local_c.json)",
		"run with --include-orphans to copy them anyway (they open empty in Desktop)",
		"unrecognized fields (copied unchanged): brandNewField",
		"transcript not found on disk (copied anyway): \"(untitled)\" (local_q.json)",
		"Manual follow-up:",
		"1 pinned sessions: 1 are or will be pinned in the target",
		"Daily check",
		"do not delete those folders",
		"cleanupPeriodDays",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestToJSONUsesEmptyArrays(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, report.ToJSON(plan.Plan{Source: srcID, Target: dstID})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "null") {
		t.Fatalf("empty lists must encode as [], got:\n%s", buf.String())
	}
	var back report.PlanJSON
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Source != srcID.String() {
		t.Fatalf("source = %q", back.Source)
	}
}

func TestToJSONNameConflicts(t *testing.T) {
	j := report.ToJSON(samplePlan())
	if j.Counts.NameConflicts != 1 || len(j.NameConflicts) != 1 || j.NameConflicts[0].File != "local_c.json" {
		t.Fatalf("name conflicts = %d %+v", j.Counts.NameConflicts, j.NameConflicts)
	}
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, j); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"name_conflicts": [`) {
		t.Fatalf("no name_conflicts key:\n%s", buf.String())
	}
}

func TestPins(t *testing.T) {
	pl := samplePlan()
	tgt := &store.Store{Records: []store.Record{{CLISessionID: "c-p", SessionID: "local_p"}}}
	rows := report.PinRows(pl, tgt)
	if len(rows) != 1 || rows[0].TargetSessionID != "local_p" || rows[0].SourceSessionID != "local_p" {
		t.Fatalf("rows = %+v", rows)
	}
	var buf bytes.Buffer
	report.Pins(&buf, rows)
	out := buf.String()
	if !strings.Contains(out, "- Pinned one  [pinned in target]") || strings.Contains(out, "Experimental") || strings.Contains(out, "Pin these sessions") {
		t.Fatalf("pins output:\n%s", out)
	}

	buf.Reset()
	report.Pins(&buf, report.PinRows(pl, &store.Store{}))
	if !strings.Contains(buf.String(), "[not migrated yet]") || strings.Contains(buf.String(), "pinned in target") {
		t.Fatalf("unmigrated pins output:\n%s", buf.String())
	}
	if want := "Run `claude-migrate apply --from <account>/<org>` first, then run pins again."; !strings.Contains(buf.String(), want) {
		t.Fatalf("unmigrated pins output lacks %q:\n%s", want, buf.String())
	}
}

// A session an older version copied under a new ID is in the target, but its
// pin did not follow: say so instead of claiming it is pinned there.
func TestPinsSessionCopiedUnderAnotherID(t *testing.T) {
	tgt := &store.Store{Records: []store.Record{{CLISessionID: "c-p", SessionID: "local_other"}}}
	rows := report.PinRows(samplePlan(), tgt)
	if len(rows) != 1 || rows[0].TargetSessionID != "local_other" {
		t.Fatalf("rows = %+v", rows)
	}
	var buf bytes.Buffer
	report.Pins(&buf, rows)
	if !strings.Contains(buf.String(), "[copied under a different ID; pin it again in Desktop]") || strings.Contains(buf.String(), "pinned in target") {
		t.Fatalf("output:\n%s", buf.String())
	}
}

func TestOrphansReport(t *testing.T) {
	j := report.ToJSON(samplePlan())
	if j.Counts.Orphans != 2 || len(j.Orphans) != 2 || j.Orphans[1].File != "local_o2.json" {
		t.Fatalf("orphans = %d %+v", j.Counts.Orphans, j.Orphans)
	}
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, j); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"orphans": 2`) {
		t.Fatalf("counts lack orphans:\n%s", buf.String())
	}
	pl := samplePlan()
	pl.Orphans = nil
	buf.Reset()
	report.Text(&buf, pl)
	if strings.Contains(buf.String(), "--include-orphans") || !strings.Contains(buf.String(), "no transcript (skipped) 0") {
		t.Fatalf("hint must appear only when something was skipped:\n%s", buf.String())
	}
}

// A pinned session that an older version copied under another ID lost its pin:
// the follow-up line says how many, and points at `pins` for the list.
func TestTextPinsToRedo(t *testing.T) {
	pl := samplePlan()
	other := store.Record{File: "local_o.json", SessionID: "local_o", CLISessionID: "c-o", Title: "Moved", Starred: true}
	pl.AlreadyInTarget = []store.Record{other}
	pl.Manual.Pins = []store.Record{pinned, other}
	pl.Manual.PinsToRedo = []store.Record{other}

	var buf bytes.Buffer
	report.Text(&buf, pl)
	want := "2 pinned sessions: 1 are or will be pinned in the target; 1 were copied earlier under a different ID and are not pinned there \u2014 pin those again in Desktop (claude-migrate pins lists them)"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, buf.String())
	}

	j := report.ToJSON(pl)
	if len(j.Manual.PinsToRedo) != 1 || j.Manual.PinsToRedo[0].File != "local_o.json" {
		t.Fatalf("pins_to_redo = %+v", j.Manual.PinsToRedo)
	}
	buf.Reset()
	if err := report.WriteJSON(&buf, report.ToJSON(samplePlan())); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"pins_to_redo": []`) {
		t.Fatalf("pins_to_redo must be [] not null:\n%s", buf.String())
	}
}

// pins must match sessions exactly as plan does: on current and prior
// cliSessionIds, on both sides.
func TestPinRowsMatchPriorCLISessionIDs(t *testing.T) {
	pl := plan.Plan{Manual: plan.Manual{Pins: []store.Record{
		{SessionID: "local_a", CLISessionID: "c-old", Title: "resumed in target"},
		{SessionID: "local_c", CLISessionID: "c-new2", PriorCLISessionIDs: []string{"c-orig"}, Title: "resumed in source"},
	}}}
	tgt := &store.Store{Records: []store.Record{
		{SessionID: "local_b", CLISessionID: "c-new", PriorCLISessionIDs: []string{"c-old"}},
		{SessionID: "local_t", CLISessionID: "c-orig"},
	}}
	rows := report.PinRows(pl, tgt)
	if rows[0].TargetSessionID != "local_b" || rows[1].TargetSessionID != "local_t" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPinsShowsCopiesAlreadyPinnedInTarget(t *testing.T) {
	pl := plan.Plan{
		Pinned: map[string]bool{"local_a": true, "local_b": true, "local_x": true},
		Manual: plan.Manual{Pins: []store.Record{
			{SessionID: "local_a", CLISessionID: "c-a", Title: "repinned"},
			{SessionID: "local_b", CLISessionID: "c-b", Title: "not repinned"},
		}},
	}
	tgt := &store.Store{Records: []store.Record{{SessionID: "local_x", CLISessionID: "c-a"}, {SessionID: "local_y", CLISessionID: "c-b"}}}
	var buf bytes.Buffer
	report.Pins(&buf, report.PinRows(pl, tgt))
	out := buf.String()
	if !strings.Contains(out, "repinned  [pinned in target]") || !strings.Contains(out, "not repinned  [copied under a different ID; pin it again in Desktop]") {
		t.Fatalf("pins output:\n%s", out)
	}
}
