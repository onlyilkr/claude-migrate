// Package report renders plans for people (text) and programs (JSON).
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/migrate"
	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/store"
)

const maxListed = 10

type Rec struct {
	File         string `json:"file"`
	CLISessionID string `json:"cli_session_id"`
	Title        string `json:"title"`
	Cwd          string `json:"cwd"`
}

type Counts struct {
	Copy              int `json:"copy"`
	AlreadyInTarget   int `json:"already_in_target"`
	Deleted           int `json:"deleted"`
	NoCLISessionID    int `json:"no_cli_session_id"`
	Duplicates        int `json:"duplicates"`
	DeletedInTarget   int `json:"deleted_in_target"`
	NameConflicts     int `json:"name_conflicts"`
	Orphans           int `json:"orphans"`
	MissingTranscript int `json:"missing_transcript"`
	Unreadable        int `json:"unreadable"`
}

type ManualJSON struct {
	Pins                  []Rec    `json:"pins"`
	PinsToRedo            []Rec    `json:"pins_to_redo"`
	ScheduledTaskSessions []Rec    `json:"scheduled_task_sessions"`
	ScheduledTasks        []string `json:"scheduled_tasks"`
	ScratchSessions       []Rec    `json:"scratch_sessions"`
	CleanupPeriodUnset    bool     `json:"cleanup_period_unset"`
}

type PlanJSON struct {
	Source            string     `json:"source"`
	Target            string     `json:"target"`
	Counts            Counts     `json:"counts"`
	ChangedFields     []string   `json:"changed_fields"`
	Copy              []Rec      `json:"copy"`
	AlreadyInTarget   []Rec      `json:"already_in_target"`
	Deleted           []Rec      `json:"deleted"`
	NoCLISessionID    []Rec      `json:"no_cli_session_id"`
	Duplicates        []Rec      `json:"duplicates"`
	DeletedInTarget   []Rec      `json:"deleted_in_target"`
	NameConflicts     []Rec      `json:"name_conflicts"`
	Orphans           []Rec      `json:"orphans"`
	MissingTranscript []Rec      `json:"missing_transcript"`
	Unreadable        []string   `json:"unreadable"`
	UnknownFields     []string   `json:"unknown_fields"`
	Manual            ManualJSON `json:"manual"`
}

type ApplyJSON struct {
	Plan     PlanJSON `json:"plan"`
	Written  int      `json:"written"`
	Manifest string   `json:"manifest,omitempty"`
}

// UndoJSON is the machine-readable result of undo. Slices are never null.
type UndoJSON struct {
	Manifest        string   `json:"manifest"`
	Removed         []string `json:"removed"`
	Missing         []string `json:"missing"`
	Modified        []string `json:"modified"`
	Rejected        []string `json:"rejected"`
	Superseded      []string `json:"superseded"`
	TempRemoved     []string `json:"temp_removed"`
	UnreadableNewer []string `json:"unreadable_newer_runs"`
	Completed       bool     `json:"completed"`
}

type DoctorAccount struct {
	AccountUUID string `json:"account_uuid"`
	OrgUUID     string `json:"org_uuid"`
	Email       string `json:"email"`
	OrgName     string `json:"org_name"`
}

type DoctorStore struct {
	ID           string   `json:"id"`
	Records      int      `json:"records"`
	LastActivity string   `json:"last_activity"` // RFC3339, or "" when unknown
	SampleTitles []string `json:"sample_titles"`
	Error        string   `json:"error,omitempty"`
}

// DoctorJSON is the machine-readable result of doctor. Each check that can fail
// carries its error beside its value instead of aborting the report.
type DoctorJSON struct {
	DesktopRunning bool           `json:"desktop_running"`
	DesktopError   string         `json:"desktop_error,omitempty"`
	Account        *DoctorAccount `json:"account"`
	AccountError   string         `json:"account_error,omitempty"`
	// DesktopLastAccount is the account Desktop last used, "" when unknown.
	// AccountsAgree is false only when both are known and differ.
	DesktopLastAccount string        `json:"desktop_last_account"`
	AccountsAgree      bool          `json:"accounts_agree"`
	CleanupPeriodSet   bool          `json:"cleanup_period_set"`
	Stores             []DoctorStore `json:"stores"`
	StoresError        string        `json:"stores_error,omitempty"`
}

func recs(rs []store.Record) []Rec {
	out := make([]Rec, 0, len(rs))
	for _, r := range rs {
		out = append(out, Rec{File: r.File, CLISessionID: r.CLISessionID, Title: r.Title, Cwd: r.Cwd})
	}
	return out
}

// Strs returns s, or an empty slice for nil, so JSON arrays are never null.
func Strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func ToUndoJSON(path string, res manifest.UndoResult) UndoJSON {
	return UndoJSON{
		Manifest: path, Removed: Strs(res.Removed), Missing: Strs(res.Missing), Modified: Strs(res.Modified),
		Rejected: Strs(res.Rejected), Superseded: Strs(res.Superseded), TempRemoved: Strs(res.TempRemoved), UnreadableNewer: Strs(res.UnreadableNewer),
		Completed: res.Completed,
	}
}

func ToJSON(pl plan.Plan) PlanJSON {
	return PlanJSON{
		Source: pl.Source.String(),
		Target: pl.Target.String(),
		Counts: Counts{
			Copy: len(pl.Copy), AlreadyInTarget: len(pl.AlreadyInTarget), Deleted: len(pl.Deleted),
			NoCLISessionID: len(pl.NoCLISessionID), Duplicates: len(pl.Duplicates), DeletedInTarget: len(pl.DeletedInTarget), NameConflicts: len(pl.NameConflicts), Orphans: len(pl.Orphans),
			MissingTranscript: len(pl.MissingTranscript), Unreadable: len(pl.Unreadable),
		},
		ChangedFields:     migrate.ChangedFields,
		Copy:              recs(pl.Copy),
		AlreadyInTarget:   recs(pl.AlreadyInTarget),
		Deleted:           recs(pl.Deleted),
		NoCLISessionID:    recs(pl.NoCLISessionID),
		Duplicates:        recs(pl.Duplicates),
		DeletedInTarget:   recs(pl.DeletedInTarget),
		NameConflicts:     recs(pl.NameConflicts),
		Orphans:           recs(pl.Orphans),
		MissingTranscript: recs(pl.MissingTranscript),
		Unreadable:        Strs(pl.Unreadable),
		UnknownFields:     Strs(pl.UnknownFields),
		Manual: ManualJSON{
			Pins:                  recs(pl.Manual.Pins),
			PinsToRedo:            recs(pl.Manual.PinsToRedo),
			ScheduledTaskSessions: recs(pl.Manual.ScheduledTaskSessions),
			ScheduledTasks:        Strs(pl.Manual.ScheduledTasks),
			ScratchSessions:       recs(pl.Manual.ScratchSessions),
			CleanupPeriodUnset:    pl.Manual.CleanupPeriodUnset,
		},
	}
}

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func label(r store.Record) string {
	t := r.Title
	if t == "" {
		t = "(untitled)"
	}
	return fmt.Sprintf("%q (%s)", t, r.File)
}

func warnEach(w io.Writer, prefix string, items []string) {
	for i, it := range items {
		if i == maxListed {
			fmt.Fprintf(w, "  ! … and %d more\n", len(items)-maxListed)
			return
		}
		fmt.Fprintf(w, "  ! %s: %s\n", prefix, it)
	}
}

func labels(rs []store.Record) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, label(r))
	}
	return out
}

// Text prints the plan summary, warnings, and manual follow-ups.
func Text(w io.Writer, pl plan.Plan) {
	c := ToJSON(pl).Counts
	fmt.Fprintf(w, "Plan: %s -> %s\n", pl.Source.Short(), pl.Target.Short())
	fmt.Fprintf(w, "  to copy %d · already in target %d · deleted %d · deleted in target %d · no cliSessionId %d · duplicates %d · name conflicts %d · no transcript (skipped) %d · transcript missing %d · unreadable %d\n",
		c.Copy, c.AlreadyInTarget, c.Deleted, c.DeletedInTarget, c.NoCLISessionID, c.Duplicates, c.NameConflicts, c.Orphans, c.MissingTranscript, c.Unreadable)
	if c.Orphans > 0 {
		fmt.Fprintln(w, "  run with --include-orphans to copy them anyway (they open empty in Desktop)")
	}
	fmt.Fprintf(w, "  fields changed on copy: %s\n", strings.Join(migrate.ChangedFields, ", "))
	if len(pl.UnknownFields) > 0 {
		fmt.Fprintf(w, "  unrecognized fields (copied unchanged): %s\n", strings.Join(pl.UnknownFields, ", "))
	}
	warnEach(w, "duplicate cliSessionId, skipped", labels(pl.Duplicates))
	warnEach(w, "deleted in the target account, skipped", labels(pl.DeletedInTarget))
	warnEach(w, "file name already used in target by another session, skipped", labels(pl.NameConflicts))
	warnEach(w, "transcript not found on disk (copied anyway)", labels(pl.MissingTranscript))
	warnEach(w, "unreadable record, skipped", pl.Unreadable)

	m := pl.Manual
	var lines []string
	if n := len(m.Pins); n > 0 {
		line := fmt.Sprintf("%d pinned sessions: %d are or will be pinned in the target", n, n-len(m.PinsToRedo))
		if y := len(m.PinsToRedo); y > 0 {
			line += fmt.Sprintf("; %d were copied earlier under a different ID and are not pinned there \u2014 pin those again in Desktop (claude-migrate pins lists them)", y)
		}
		lines = append(lines, line)
	}
	if len(m.ScheduledTasks) > 0 {
		lines = append(lines, "scheduled tasks to recreate in the target account: "+strings.Join(m.ScheduledTasks, ", "))
	}
	if n := len(m.ScheduledTaskSessions); n > 0 {
		lines = append(lines, fmt.Sprintf("%d sessions were linked to a scheduled task; the link is removed", n))
	}
	if n := len(m.ScratchSessions); n > 0 {
		lines = append(lines, fmt.Sprintf("%d sessions run in the old account's scratch folders; do not delete those folders", n))
	}
	if m.CleanupPeriodUnset {
		lines = append(lines, "cleanupPeriodDays is not set in ~/.claude/settings.json; Claude Code deletes transcripts older than 30 days")
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "\nManual follow-up:")
	for _, l := range lines {
		fmt.Fprintf(w, "  • %s\n", l)
	}
}
