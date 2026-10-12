package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/paths"
)

var (
	ErrNoMatch   = errors.New("no session store matches")
	ErrAmbiguous = errors.New("store reference is ambiguous")
)

// Info summarizes a store for listing and selection.
type Info struct {
	ID           ID
	Dir          string
	Records      int
	LastActivity time.Time
	SampleTitles []string // up to two, most recent first
	// Err is why the store could not be read; the other fields are then empty.
	// Discover records it here instead of failing, so one bad store does not hide the rest.
	Err error
}

// Discover lists every <account>/<org> store, most recently active first.
func Discover(p paths.Paths) ([]Info, error) {
	root := p.SessionsRoot()
	accounts, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s not found (is Claude Desktop installed and signed in?)", ErrLayout, root)
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, a := range accounts {
		if !a.IsDir() || !paths.IsUUID(a.Name()) {
			continue
		}
		orgs, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			// Like an unreadable store: record it, never match it, keep the rest.
			out = append(out, Info{ID: ID{Account: a.Name()}, Dir: filepath.Join(root, a.Name()), Err: err})
			continue
		}
		for _, o := range orgs {
			if !o.IsDir() || !paths.IsUUID(o.Name()) {
				continue
			}
			id := ID{Account: a.Name(), Org: o.Name()}
			s, err := Load(p, id)
			if err != nil {
				out = append(out, Info{ID: id, Dir: p.StoreDir(id.Account, id.Org), Err: err})
				continue
			}
			out = append(out, summarize(s))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no <account>/<org> directories under %s", ErrLayout, root)
	}
	slices.SortStableFunc(out, func(x, y Info) int { return y.LastActivity.Compare(x.LastActivity) })
	return out, nil
}

func summarize(s *Store) Info {
	in := Info{ID: s.ID, Dir: s.Dir, Records: len(s.Records)}
	recs := slices.Clone(s.Records)
	slices.SortStableFunc(recs, func(x, y Record) int { return y.LastActivity.Compare(x.LastActivity) })
	for _, r := range recs {
		if r.LastActivity.After(in.LastActivity) {
			in.LastActivity = r.LastActivity
		}
		if r.Title != "" && len(in.SampleTitles) < 2 {
			in.SampleTitles = append(in.SampleTitles, r.Title)
		}
	}
	return in
}

// Resolve matches "<account>/<org>" (each part may be a prefix) to exactly one store.
func Resolve(infos []Info, ref string) (ID, error) {
	acc, org, ok := strings.Cut(ref, "/")
	if !ok || acc == "" || org == "" {
		return ID{}, fmt.Errorf("%w: %q is not <account>/<org>", ErrNoMatch, ref)
	}
	var found []ID
	for _, in := range infos {
		if strings.HasPrefix(in.ID.Account, acc) && strings.HasPrefix(in.ID.Org, org) {
			found = append(found, in.ID)
		}
	}
	switch len(found) {
	case 0:
		return ID{}, fmt.Errorf("%w: %q", ErrNoMatch, ref)
	case 1:
		return found[0], nil
	}
	return ID{}, fmt.Errorf("%w: %q matches %d stores; use a longer prefix", ErrAmbiguous, ref, len(found))
}

// TranscriptIndex returns the set of cliSessionIds with a transcript under ~/.claude/projects.
func TranscriptIndex(p paths.Paths) (map[string]bool, error) {
	matches, err := filepath.Glob(filepath.Join(p.ProjectsDir(), "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	idx := make(map[string]bool, len(matches))
	for _, m := range matches {
		idx[strings.TrimSuffix(filepath.Base(m), ".jsonl")] = true
	}
	return idx, nil
}
