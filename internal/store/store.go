// Package store reads Claude Desktop's per-account session stores. It is the only
// package that knows the on-disk format, so format changes are fixed here.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/paths"
)

var (
	ErrLayout       = errors.New("unrecognized Claude Desktop session layout")
	ErrStoreMissing = errors.New("session store directory does not exist")
)

// ID names one account×organization store.
type ID struct{ Account, Org string }

func (id ID) String() string { return id.Account + "/" + id.Org }
func (id ID) Short() string  { return ShortUUID(id.Account) + "/" + ShortUUID(id.Org) }

// ShortUUID returns the first 8 characters of s (or s if shorter).
func ShortUUID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Record is one local_<id>.json file. Raw holds every field untouched; the
// typed fields are convenience views of a few of them.
type Record struct {
	File         string
	Raw          map[string]json.RawMessage
	SessionID    string
	CLISessionID string
	// PriorCLISessionIDs are earlier cliSessionIds of the same session;
	// Desktop moves the old id here when a session is resumed.
	PriorCLISessionIDs []string
	Title              string
	Cwd                string
	ScheduledTaskID    string
	Starred            bool
	LastActivity       time.Time
}

type Store struct {
	ID             ID
	Dir            string
	Records        []Record        // sorted by file name
	Unreadable     []string        // local_*.json names that are not regular files holding a JSON object
	Deleted        map[string]bool // ids from deleted_<id>[.json] markers
	ScheduledTasks []string        // display names from scheduled-tasks.json
}

// IsDeleted reports whether Desktop marked r as deleted.
func (s *Store) IsDeleted(r Record) bool {
	return s.Deleted[strings.TrimSuffix(strings.TrimPrefix(r.File, "local_"), ".json")]
}

// Load reads one store. It never writes.
func Load(p paths.Paths, id ID) (*Store, error) {
	dir := p.StoreDir(id.Account, id.Org)
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s (sign in to that account in Claude Desktop once)", ErrStoreMissing, dir)
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrLayout, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{ID: id, Dir: dir, Deleted: map[string]bool{}}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "deleted_"):
			s.Deleted[strings.TrimSuffix(strings.TrimPrefix(name, "deleted_"), ".json")] = true
		case strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json") && !e.Type().IsRegular():
			// A symlink, directory or other entry still occupies the name.
			s.Unreadable = append(s.Unreadable, name)
		case strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json"):
			r, err := parseRecord(filepath.Join(dir, name))
			if err != nil {
				s.Unreadable = append(s.Unreadable, name)
				continue
			}
			s.Records = append(s.Records, r)
		case name == "scheduled-tasks.json":
			s.ScheduledTasks = readScheduledTasks(filepath.Join(dir, name))
		}
	}
	return s, nil
}

func parseRecord(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return Record{}, err
	}
	if raw == nil {
		return Record{}, errors.New("not a JSON object")
	}
	r := Record{
		File:               filepath.Base(path),
		Raw:                raw,
		SessionID:          str(raw, "sessionId"),
		CLISessionID:       str(raw, "cliSessionId"),
		Title:              str(raw, "title"),
		Cwd:                str(raw, "cwd"),
		ScheduledTaskID:    str(raw, "scheduledTaskId"),
		PriorCLISessionIDs: strs(raw, "priorCliSessionIds"),
	}
	_ = json.Unmarshal(raw["isStarred"], &r.Starred)
	var ms int64
	if json.Unmarshal(raw["lastActivityAt"], &ms) == nil && ms > 0 {
		r.LastActivity = time.UnixMilli(ms)
	}
	return r, nil
}

// str returns raw[key] if it is a JSON string, else "".
func str(raw map[string]json.RawMessage, key string) string {
	var s string
	if v, ok := raw[key]; ok {
		if json.Unmarshal(v, &s) != nil {
			return ""
		}
	}
	return s
}

// strs returns the string elements of raw[key] if it is a JSON array, else nil.
func strs(raw map[string]json.RawMessage, key string) []string {
	var items []json.RawMessage
	if json.Unmarshal(raw[key], &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func readScheduledTasks(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		ScheduledTasks []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"scheduledTasks"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var names []string
	for _, t := range doc.ScheduledTasks {
		if t.DisplayName != "" {
			names = append(names, t.DisplayName)
		} else {
			names = append(names, t.ID)
		}
	}
	return names
}
