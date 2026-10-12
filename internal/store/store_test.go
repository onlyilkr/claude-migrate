package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onlyilkr/claude-migrate/internal/store"
	"github.com/onlyilkr/claude-migrate/internal/testutil"
)

var oldID = store.ID{Account: testutil.AccOld, Org: testutil.OrgOld}

func files(rs []store.Record) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.File)
	}
	return out
}

func TestLoad(t *testing.T) {
	h := testutil.NewHome(t)
	dir := h.Store(testutil.AccOld, testutil.OrgOld)
	h.Record(testutil.AccOld, testutil.OrgOld, "b", map[string]any{
		"sessionId": "local_b", "cliSessionId": "cli-b", "title": "B", "cwd": "/w/b",
		"isStarred": true, "scheduledTaskId": "task-1", "lastActivityAt": int64(1700000000000),
	})
	h.Record(testutil.AccOld, testutil.OrgOld, "a", map[string]any{"sessionId": "local_a", "cliSessionId": "cli-a"})
	h.Record(testutil.AccOld, testutil.OrgOld, "num", map[string]any{"cliSessionId": 42})
	h.Record(testutil.AccOld, testutil.OrgOld, "nul", map[string]any{"cliSessionId": nil})
	h.File(dir, "local_bad.json", "{not json")
	h.File(dir, "local_arr.json", "[1,2]")
	h.File(dir, "local_null.json", "null")
	h.File(dir, "deleted_a", "")
	h.File(dir, "deleted_x.json", "")
	h.File(dir, "notes.txt", "ignored")
	h.File(filepath.Join(dir, "backlog"), "item.json", "{}")
	h.File(dir, "scheduled-tasks.json", `{"scheduledTasks":[{"id":"t1","displayName":"Daily check"},{"id":"t2"}]}`)

	s, err := store.Load(h.P, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := files(s.Records), []string{"local_a.json", "local_b.json", "local_nul.json", "local_num.json"}; !slices.Equal(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	b := s.Records[1]
	if b.CLISessionID != "cli-b" || b.Title != "B" || b.Cwd != "/w/b" || !b.Starred ||
		b.ScheduledTaskID != "task-1" || b.SessionID != "local_b" || !b.LastActivity.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("record b parsed wrong: %+v", b)
	}
	if s.Records[2].CLISessionID != "" || s.Records[3].CLISessionID != "" {
		t.Fatal("null/number cliSessionId must parse as empty")
	}
	if got, want := s.Unreadable, []string{"local_arr.json", "local_bad.json", "local_null.json"}; !slices.Equal(got, want) {
		t.Fatalf("unreadable = %v, want %v", got, want)
	}
	if !s.IsDeleted(s.Records[0]) || s.IsDeleted(s.Records[1]) || !s.Deleted["x"] {
		t.Fatalf("deleted markers wrong: %v", s.Deleted)
	}
	if got, want := s.ScheduledTasks, []string{"Daily check", "t2"}; !slices.Equal(got, want) {
		t.Fatalf("scheduled tasks = %v, want %v", got, want)
	}
	if string(b.Raw["title"]) != `"B"` {
		t.Fatalf("raw title = %s", b.Raw["title"])
	}
}

func TestLoadMissing(t *testing.T) {
	h := testutil.NewHome(t)
	if _, err := store.Load(h.P, oldID); !errors.Is(err, store.ErrStoreMissing) {
		t.Fatalf("err = %v, want ErrStoreMissing", err)
	}
}

func TestIDFormatting(t *testing.T) {
	if oldID.String() != testutil.AccOld+"/"+testutil.OrgOld {
		t.Fatal(oldID.String())
	}
	if oldID.Short() != "a1a1a1a1/b1b1b1b1" {
		t.Fatal(oldID.Short())
	}
	if store.ShortUUID("abc") != "abc" {
		t.Fatal("short input must pass through")
	}
}

func TestLoadPriorCLISessionIDs(t *testing.T) {
	h := testutil.NewHome(t)
	h.Record(testutil.AccOld, testutil.OrgOld, "a", map[string]any{"cliSessionId": "c-a", "priorCliSessionIds": []any{"p1", 5, nil, "p2"}})
	h.Record(testutil.AccOld, testutil.OrgOld, "b", map[string]any{"cliSessionId": "c-b", "priorCliSessionIds": "not-a-list"})
	s, err := store.Load(h.P, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Records[0].PriorCLISessionIDs; !slices.Equal(got, []string{"p1", "p2"}) {
		t.Fatalf("prior ids = %v, want [p1 p2]", got)
	}
	if got := s.Records[1].PriorCLISessionIDs; len(got) != 0 {
		t.Fatalf("non-list prior ids = %v, want none", got)
	}
}

// A local_*.json entry that is not a regular file (symlink, directory) cannot
// be read as a record, but it still occupies its name: report it, never ignore it.
func TestLoadReportsNonRegularRecordEntriesAsUnreadable(t *testing.T) {
	h := testutil.NewHome(t)
	dir := h.Store(testutil.AccOld, testutil.OrgOld)
	good := h.Record(testutil.AccOld, testutil.OrgOld, "ok", map[string]any{"cliSessionId": "cli-ok"})
	if err := os.Symlink(good, filepath.Join(dir, "local_link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "local_dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	s, err := store.Load(h.P, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if got := files(s.Records); !slices.Equal(got, []string{"local_ok.json"}) {
		t.Fatalf("records = %v", got)
	}
	slices.Sort(s.Unreadable)
	if want := []string{"local_dir.json", "local_link.json"}; !slices.Equal(s.Unreadable, want) {
		t.Fatalf("unreadable = %v, want %v", s.Unreadable, want)
	}
}
