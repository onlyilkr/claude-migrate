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

func TestDiscover(t *testing.T) {
	h := testutil.NewHome(t)
	h.Record(testutil.AccOld, testutil.OrgOld, "1", map[string]any{"cliSessionId": "c1", "title": "Old one", "lastActivityAt": 1000})
	h.Record(testutil.AccOld, testutil.OrgOld, "2", map[string]any{"cliSessionId": "c2", "title": "Old two", "lastActivityAt": 3000})
	h.Record(testutil.AccOld, testutil.OrgOld, "3", map[string]any{"cliSessionId": "c3", "title": "Old three", "lastActivityAt": 2000})
	h.Store(testutil.AccNew, testutil.OrgNew)
	h.File(h.P.SessionsRoot(), ".DS_Store", "")
	h.Store("not-a-uuid", "x")

	infos, err := store.Discover(h.P)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d stores, want 2: %+v", len(infos), infos)
	}
	old := infos[0]
	if old.ID != oldID || old.Records != 3 || !old.LastActivity.Equal(time.UnixMilli(3000)) {
		t.Fatalf("old store info wrong: %+v", old)
	}
	if want := []string{"Old two", "Old three"}; !slices.Equal(old.SampleTitles, want) {
		t.Fatalf("sample titles = %v, want %v", old.SampleTitles, want)
	}
	if infos[1].Records != 0 {
		t.Fatalf("empty store: %+v", infos[1])
	}
}

func TestDiscoverLayoutErrors(t *testing.T) {
	h := testutil.NewHome(t)
	if _, err := store.Discover(h.P); !errors.Is(err, store.ErrLayout) {
		t.Fatalf("no root: err = %v, want ErrLayout", err)
	}
	if err := os.MkdirAll(h.P.SessionsRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Discover(h.P); !errors.Is(err, store.ErrLayout) {
		t.Fatalf("empty root: err = %v, want ErrLayout", err)
	}
}

func TestResolve(t *testing.T) {
	a := store.ID{Account: "aaaa1111-0000-4000-8000-000000000000", Org: "bbbb1111-0000-4000-8000-000000000000"}
	b := store.ID{Account: "aaaa2222-0000-4000-8000-000000000000", Org: "bbbb1111-0000-4000-8000-000000000000"}
	infos := []store.Info{{ID: a}, {ID: b}}

	if got, err := store.Resolve(infos, "aaaa1/bbbb"); err != nil || got != a {
		t.Fatalf("prefix: got %v, %v", got, err)
	}
	if got, err := store.Resolve(infos, a.String()); err != nil || got != a {
		t.Fatalf("full: got %v, %v", got, err)
	}
	if _, err := store.Resolve(infos, "aaaa/bbbb"); !errors.Is(err, store.ErrAmbiguous) {
		t.Fatalf("ambiguous: err = %v", err)
	}
	for _, ref := range []string{"zzzz/bbbb", "aaaa1111", "/bbbb", "aaaa1/"} {
		if _, err := store.Resolve(infos, ref); !errors.Is(err, store.ErrNoMatch) {
			t.Fatalf("%q: err = %v, want ErrNoMatch", ref, err)
		}
	}
}

func TestTranscriptIndex(t *testing.T) {
	h := testutil.NewHome(t)
	idx, err := store.TranscriptIndex(h.P)
	if err != nil || len(idx) != 0 {
		t.Fatalf("missing dir: %v, %v", idx, err)
	}
	h.Transcript("-w-a", "c1")
	h.Transcript("-w-b", "c2")
	idx, err = store.TranscriptIndex(h.P)
	if err != nil {
		t.Fatal(err)
	}
	if !idx["c1"] || !idx["c2"] || idx["c3"] {
		t.Fatalf("index = %v", idx)
	}
}

// One store that cannot be read must not hide the others.
func TestDiscoverKeepsUnreadableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := testutil.NewHome(t)
	h.Record(testutil.AccOld, testutil.OrgOld, "1", map[string]any{"cliSessionId": "c1", "title": "Good"})
	bad := h.Store(testutil.AccNew, testutil.OrgNew)
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o700) })

	infos, err := store.Discover(h.P)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d stores, want 2: %+v", len(infos), infos)
	}
	var good, broken store.Info
	for _, in := range infos {
		if in.Err != nil {
			broken = in
		} else {
			good = in
		}
	}
	if good.ID != oldID || good.Records != 1 {
		t.Fatalf("good store: %+v", good)
	}
	if broken.ID != (store.ID{Account: testutil.AccNew, Org: testutil.OrgNew}) || broken.Dir != bad || broken.Records != 0 {
		t.Fatalf("unreadable store: %+v", broken)
	}
}

// An <account> directory that cannot be listed must not abort discovery either:
// it is reported as an unreadable entry that can never be picked or resolved.
func TestDiscoverKeepsUnreadableAccountDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := testutil.NewHome(t)
	h.Record(testutil.AccOld, testutil.OrgOld, "1", map[string]any{"cliSessionId": "c1", "title": "Good"})
	const badAcc = "a3a3a3a3-0000-4000-8000-000000000003"
	badDir := filepath.Join(h.P.SessionsRoot(), badAcc)
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(badDir, 0o700) })

	infos, err := store.Discover(h.P)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(infos), infos)
	}
	var broken store.Info
	for _, in := range infos {
		if in.Err != nil {
			broken = in
		}
	}
	if broken.ID != (store.ID{Account: badAcc}) || broken.Dir != badDir || broken.Records != 0 {
		t.Fatalf("unreadable account: %+v", broken)
	}
	for _, ref := range []string{"a3a3a3a3/", "a3a3a3a3/b", badAcc + "/" + testutil.OrgOld} {
		if _, err := store.Resolve(infos, ref); !errors.Is(err, store.ErrNoMatch) {
			t.Fatalf("%q must not resolve: err = %v", ref, err)
		}
	}
}
