package report

import (
	"fmt"
	"io"

	"github.com/onlyilkr/claude-migrate/internal/plan"
	"github.com/onlyilkr/claude-migrate/internal/store"
)

type PinRow struct {
	Title           string `json:"title"`
	Cwd             string `json:"cwd"`
	SourceSessionID string `json:"source_session_id"`
	TargetSessionID string `json:"target_session_id,omitempty"` // equals source_session_id once copied; empty: not migrated yet
	TargetPinned    bool   `json:"target_pinned"`               // the target's record is in Desktop's pin list
}

// PinRows maps each pinned source session to its record in the target, if any.
// A record with the same session ID is the normal case (IDs are kept). Other
// matches use every cliSessionId a session has had, on both sides, exactly as
// plan does; those were copied by an older version under a new ID.
func PinRows(pl plan.Plan, target *store.Store) []PinRow {
	byID := map[string]bool{}
	byCLI := map[string]string{}
	for _, r := range target.Records {
		byID[r.SessionID] = true
		for _, id := range append([]string{r.CLISessionID}, r.PriorCLISessionIDs...) {
			if id != "" {
				byCLI[id] = r.SessionID
			}
		}
	}
	rows := make([]PinRow, 0, len(pl.Manual.Pins))
	for _, r := range pl.Manual.Pins {
		row := PinRow{Title: r.Title, Cwd: r.Cwd, SourceSessionID: r.SessionID}
		if byID[r.SessionID] {
			row.TargetSessionID = r.SessionID
		} else {
			for _, id := range append([]string{r.CLISessionID}, r.PriorCLISessionIDs...) {
				if t, ok := byCLI[id]; ok && id != "" {
					row.TargetSessionID = t
					break
				}
			}
		}
		row.TargetPinned = row.TargetSessionID != "" && (row.TargetSessionID == r.SessionID || pl.Pinned[row.TargetSessionID])
		rows = append(rows, row)
	}
	return rows
}

func titleOr(t string) string {
	if t == "" {
		return "(untitled)"
	}
	return t
}

func status(r PinRow) string {
	switch r.TargetSessionID {
	case "":
		return "not migrated yet"
	case r.SourceSessionID:
		return "pinned in target"
	}
	if r.TargetPinned {
		return "pinned in target"
	}
	return "copied under a different ID; pin it again in Desktop"
}

func Pins(w io.Writer, rows []PinRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No pinned sessions in the source store.")
		return
	}
	pending := false
	fmt.Fprintf(w, "%d pinned sessions in the source store:\n", len(rows))
	for _, r := range rows {
		pending = pending || r.TargetSessionID == ""
		fmt.Fprintf(w, "  - %s  [%s]\n", titleOr(r.Title), status(r))
	}
	if pending {
		fmt.Fprintln(w, "\nRun `claude-migrate apply` first, then run pins again.")
	}
}
