package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type UndoResult struct {
	Removed    []string // deleted: content still matched what apply wrote
	Missing    []string // already gone (or never landed)
	Modified   []string // kept: changed since apply (removed only with force)
	Rejected   []string // kept: entry points outside the target store or at a non-record file
	Superseded []string // kept: a newer run recorded the same path and owns it now
	// UnreadableNewer lists newer runs that could not be read, so they claim
	// nothing; a file one of them re-recorded may still be removed here.
	UnreadableNewer []string
	TempRemoved     []string // leftover .claude-migrate-*.tmp files cleaned up
	Completed       bool     // nothing was kept; the run file was renamed to .undone
}

// Undo removes the files a run created. It only touches local_*.json files
// directly inside the run's target store whose hash still matches, unless force.
func Undo(path string, force bool) (UndoResult, error) {
	var res UndoResult
	h, entries, err := Read(path)
	if err != nil {
		return res, err
	}
	if h.TargetDir == "" {
		return res, fmt.Errorf("%s: manifest has no target_dir", path)
	}
	target := filepath.Clean(h.TargetDir)
	retracted := map[string]bool{}
	for _, e := range entries {
		if e.Retracted {
			retracted[filepath.Clean(e.Path)] = true
		}
	}
	newer, unreadable := newerRunPaths(path)
	res.UnreadableNewer = unreadable
	for _, e := range entries {
		if e.Retracted || retracted[filepath.Clean(e.Path)] {
			continue
		}
		base := filepath.Base(e.Path)
		if filepath.Dir(filepath.Clean(e.Path)) != target || !strings.HasPrefix(base, "local_") || !strings.HasSuffix(base, ".json") {
			res.Rejected = append(res.Rejected, e.Path)
			continue
		}
		if newer[filepath.Clean(e.Path)] {
			res.Superseded = append(res.Superseded, e.Path)
			continue
		}
		b, err := os.ReadFile(e.Path)
		if errors.Is(err, fs.ErrNotExist) {
			res.Missing = append(res.Missing, e.Path)
			continue
		}
		if err != nil {
			return res, err
		}
		if HashHex(b) != e.SHA256 && !force {
			res.Modified = append(res.Modified, e.Path)
			continue
		}
		if err := os.Remove(e.Path); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, e.Path)
	}
	temps, err := filepath.Glob(filepath.Join(target, ".claude-migrate-*.tmp"))
	if err != nil {
		return res, err
	}
	for _, t := range temps {
		if err := os.Remove(t); err != nil {
			return res, err
		}
		res.TempRemoved = append(res.TempRemoved, t)
	}
	if len(res.Modified) == 0 && len(res.Rejected) == 0 {
		if err := os.Rename(path, path+".undone"); err != nil {
			return res, err
		}
		res.Completed = true
	}
	return res, nil
}

// newerRunPaths returns the paths that runs newer than the one at path still
// record (retracted entries excluded). Run files are named by start time, so a
// name that sorts after this one is newer. Undone and unreadable runs claim
// nothing. With kept session IDs two runs can record one path with one hash;
// the newer run owns it, so undoing the older run must not delete it.
func newerRunPaths(path string) (owned map[string]bool, unreadable []string) {
	owned = map[string]bool{}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.jsonl"))
	if err != nil {
		return owned, nil
	}
	self := filepath.Base(path)
	for _, m := range matches {
		if filepath.Base(m) <= self {
			continue
		}
		_, entries, err := Read(m)
		if err != nil {
			unreadable = append(unreadable, m)
			continue
		}
		retracted := map[string]bool{}
		for _, e := range entries {
			if e.Retracted {
				retracted[filepath.Clean(e.Path)] = true
			}
		}
		for _, e := range entries {
			if p := filepath.Clean(e.Path); !e.Retracted && !retracted[p] {
				owned[p] = true
			}
		}
	}
	return owned, unreadable
}
