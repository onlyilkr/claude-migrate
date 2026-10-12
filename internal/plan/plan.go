// Package plan decides what a migration would do. It is pure: no I/O.
package plan

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/onlyilkr/claude-migrate/internal/store"
)

type Input struct {
	Source, Target   *store.Store
	Transcripts      map[string]bool // cliSessionIds with a transcript on disk
	ScratchRoot      string          // paths.ScratchRoot()
	CleanupPeriodSet bool
	IncludeOrphans   bool // copy records whose transcript is missing too
	// Pinned is Desktop's global pin list ("local_<id>" session IDs). Nil means
	// the list could not be read: a record's own isStarred flag decides instead.
	Pinned map[string]bool
}

type Plan struct {
	Source, Target    store.ID
	Pinned            map[string]bool // Desktop's pin list (Input.Pinned); nil when it could not be read
	TargetDir         string
	Copy              []store.Record
	AlreadyInTarget   []store.Record
	Deleted           []store.Record
	NoCLISessionID    []store.Record
	DeletedInTarget   []store.Record // the user deleted this session in the target account (deleted_<id> marker there)
	NameConflicts     []store.Record // file name already used in the target by another session
	Duplicates        []store.Record
	Orphans           []store.Record // skipped: no transcript on disk
	MissingTranscript []store.Record // subset of Copy; only with IncludeOrphans
	Unreadable        []string
	UnknownFields     []string // fields in Copy records not in store.KnownFields
	Manual            Manual
}

// Manual lists follow-ups the tool reports but does not perform.
type Manual struct {
	Pins                  []store.Record // pinned, among Copy and AlreadyInTarget
	PinsToRedo            []store.Record // pinned records already in the target under another file name: the pin did not follow
	ScheduledTaskSessions []store.Record // Copy records whose scheduledTaskId is removed
	ScheduledTasks        []string       // tasks in the source store to recreate
	ScratchSessions       []store.Record // cwd inside the source account's scratch folders
	CleanupPeriodUnset    bool
}

// SkippedOther counts the source records left out for a reason other than being
// in the target already: deleted (in either account), no cliSessionId,
// duplicates, name conflicts, orphans and unreadable files.
func (pl Plan) SkippedOther() int {
	return len(pl.Deleted) + len(pl.DeletedInTarget) + len(pl.NoCLISessionID) + len(pl.Duplicates) +
		len(pl.NameConflicts) + len(pl.Orphans) + len(pl.Unreadable)
}

// Build applies the skip rules in this order: no cliSessionId, deleted,
// already in target, file-name conflict, duplicate within source, no
// transcript on disk (skipped unless IncludeOrphans).
func Build(in Input) Plan {
	pl := Plan{
		Source:     in.Source.ID,
		Target:     in.Target.ID,
		TargetDir:  in.Target.Dir,
		Unreadable: slices.Clone(in.Source.Unreadable),
	}
	// A session keeps its identity across resumes through priorCliSessionIds,
	// so match on every id it has had, on both sides.
	inTarget := map[string]bool{}
	for _, r := range in.Target.Records {
		for _, id := range append([]string{r.CLISessionID}, r.PriorCLISessionIDs...) {
			if id != "" {
				inTarget[id] = true
			}
		}
	}
	migrated := func(r store.Record) bool {
		return inTarget[r.CLISessionID] || slices.ContainsFunc(r.PriorCLISessionIDs, func(id string) bool { return inTarget[id] })
	}
	// A copy keeps its file name, so any name the target already uses (even an
	// unreadable file) is taken.
	taken := map[string]bool{}
	for _, r := range in.Target.Records {
		taken[r.File] = true
	}
	for _, name := range in.Target.Unreadable {
		taken[name] = true
	}
	queued := map[string]bool{}
	unknown := map[string]bool{}
	var live []store.Record
	for _, r := range in.Source.Records {
		switch {
		case r.CLISessionID == "":
			pl.NoCLISessionID = append(pl.NoCLISessionID, r)
		case in.Source.IsDeleted(r):
			pl.Deleted = append(pl.Deleted, r)
		case migrated(r):
			pl.AlreadyInTarget = append(pl.AlreadyInTarget, r)
			live = append(live, r)
		case in.Target.IsDeleted(r):
			pl.DeletedInTarget = append(pl.DeletedInTarget, r)
		case taken[r.File]:
			pl.NameConflicts = append(pl.NameConflicts, r)
		case queued[r.CLISessionID]:
			pl.Duplicates = append(pl.Duplicates, r)
		case !in.Transcripts[r.CLISessionID] && !in.IncludeOrphans:
			queued[r.CLISessionID] = true // a second record of this session is a duplicate, not another orphan
			pl.Orphans = append(pl.Orphans, r)
		default:
			queued[r.CLISessionID] = true
			pl.Copy = append(pl.Copy, r)
			live = append(live, r)
			if !in.Transcripts[r.CLISessionID] {
				pl.MissingTranscript = append(pl.MissingTranscript, r)
			}
			if r.ScheduledTaskID != "" {
				pl.Manual.ScheduledTaskSessions = append(pl.Manual.ScheduledTaskSessions, r)
			}
			for k := range r.Raw {
				if !store.KnownFields[k] {
					unknown[k] = true
				}
			}
		}
	}
	scratchPrefix := filepath.Join(in.ScratchRoot, in.Source.ID.Account) + string(filepath.Separator)
	pl.Pinned = in.Pinned
	// The target ID of every session the target holds, by each cliSessionId it
	// has had, to tell whether a copy under another ID is already pinned there.
	targetID := map[string]string{}
	for _, r := range in.Target.Records {
		for _, id := range append([]string{r.CLISessionID}, r.PriorCLISessionIDs...) {
			if id != "" {
				targetID[id] = r.SessionID
			}
		}
	}
	repinned := func(r store.Record) bool {
		for _, id := range append([]string{r.CLISessionID}, r.PriorCLISessionIDs...) {
			if t, ok := targetID[id]; ok && in.Pinned[t] {
				return true
			}
		}
		return false
	}
	inTargetByName := map[string]bool{}
	for _, r := range in.Target.Records {
		inTargetByName[r.File] = true
	}
	pinned := func(r store.Record) bool {
		if in.Pinned == nil {
			return r.Starred
		}
		return in.Pinned[r.SessionID]
	}
	for _, r := range live {
		if pinned(r) {
			pl.Manual.Pins = append(pl.Manual.Pins, r)
			// Present in the target, but not under this name: Desktop's pin
			// names the source ID, so the copy under another ID is not pinned.
			if migrated(r) && !inTargetByName[r.File] && !repinned(r) {
				pl.Manual.PinsToRedo = append(pl.Manual.PinsToRedo, r)
			}
		}
		if strings.HasPrefix(r.Cwd, scratchPrefix) {
			pl.Manual.ScratchSessions = append(pl.Manual.ScratchSessions, r)
		}
	}
	for k := range unknown {
		pl.UnknownFields = append(pl.UnknownFields, k)
	}
	sort.Strings(pl.UnknownFields)
	pl.Manual.ScheduledTasks = slices.Clone(in.Source.ScheduledTasks)
	pl.Manual.CleanupPeriodUnset = !in.CleanupPeriodSet
	return pl
}
