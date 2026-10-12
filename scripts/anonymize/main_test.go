package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const realID = "11111111-2222-4333-8444-555555555555"

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func keys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func newTestAnon(salt string) *anon { return newAnon("acct", []byte(salt)) }

// Top-level record keys are the schema and stay; every key below them can carry
// user data (paths, names, ids) and is renamed.
func TestNestedKeysRenamedTopLevelKept(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	write(t, src, "local_"+realID+".json",
		`{"title":"secret title","cwd":"/Users/me/work","env":{"MY_SECRET_TOKEN":"v","nested":{"deepKey":1}},"list":[{"/Users/me/work/file.go":true}]}`)
	if err := run(newTestAnon("s"), src, out); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(out, "local_*.json"))
	if len(files) != 1 {
		t.Fatalf("outputs: %v", files)
	}
	doc := readJSON(t, files[0])
	if want := []string{"cwd", "env", "list", "title"}; !slices.Equal(keys(doc), want) {
		t.Fatalf("top-level keys = %v, want %v", keys(doc), want)
	}
	raw, _ := os.ReadFile(files[0])
	for _, leak := range []string{"MY_SECRET_TOKEN", "deepKey", "file.go", "/Users/me", "secret title", realID} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("output still contains %q:\n%s", leak, raw)
		}
	}
	env := doc["env"].(map[string]any)
	if len(env) != 2 {
		t.Fatalf("nested map must keep its shape, got %v", env)
	}
	for k := range env {
		if !strings.HasPrefix(k, "key-") {
			t.Errorf("nested key %q not renamed", k)
		}
	}
}

// scheduled-tasks.json: the tool reads id/displayName from each task, so the
// task's own keys stay; maps deeper than that are renamed like everything else.
func TestScheduledTasksKeepsSchemaKeys(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	write(t, src, "scheduled-tasks.json",
		`{"scheduledTasks":[{"id":"task-1","displayName":"Nightly","cwd":"/Users/me/x"}],"recordedSkips":{"task-1":{"when":"yesterday"}},"flag":true}`)
	if err := run(newTestAnon("s"), src, out); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(out, "scheduled-tasks.json"))
	if want := []string{"flag", "recordedSkips", "scheduledTasks"}; !slices.Equal(keys(doc), want) {
		t.Fatalf("top-level keys = %v, want %v", keys(doc), want)
	}
	task := doc["scheduledTasks"].([]any)[0].(map[string]any)
	if want := []string{"cwd", "displayName", "id"}; !slices.Equal(keys(task), want) {
		t.Fatalf("task keys = %v, want %v", keys(task), want)
	}
	skips := doc["recordedSkips"].(map[string]any)
	for k, v := range skips {
		// The key was the task id; it maps like the id value so the reference survives.
		if k == "task-1" || k != task["id"] {
			t.Errorf("recordedSkips key %q, want the anonymized task id %q", k, task["id"])
		}
		for k2 := range v.(map[string]any) {
			if k2 == "when" {
				t.Errorf("deeper key not renamed")
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join(out, "scheduled-tasks.json"))
	if strings.Contains(string(raw), "Nightly") || strings.Contains(string(raw), "/Users/me") {
		t.Fatalf("values not anonymized:\n%s", raw)
	}
}

func TestFakeUUIDStableWithinRunAndSalted(t *testing.T) {
	a := newTestAnon("salt-one")
	first, second := a.fakeUUID(realID), a.fakeUUID(realID)
	if first != second {
		t.Fatal("same input must map to the same fake within one anon")
	}
	if a.str("k", realID) != a.fakeUUID(realID) {
		t.Fatal("str must use the salted fakeUUID for uuid values")
	}
	b := newTestAnon("salt-two")
	if a.fakeUUID(realID) == b.fakeUUID(realID) {
		t.Fatal("different salts must give different fakes")
	}
	if !uuidRe.MatchString(a.fakeUUID(realID)) {
		t.Fatalf("fake %q is not a uuid", a.fakeUUID(realID))
	}
}

// deleted_ markers and local_ file names carry the same real id and must still
// line up after anonymization.
func TestDeletedMarkerAndRecordNameAgree(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	write(t, src, "local_"+realID+".json", `{"title":"t"}`)
	write(t, src, "deleted_"+realID, "")
	if err := run(newTestAnon("s"), src, out); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	id := func(name, prefix string) string {
		return strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
	}
	var local, deleted string
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "local_"):
			local = id(e.Name(), "local_")
		case strings.HasPrefix(e.Name(), "deleted_"):
			deleted = id(e.Name(), "deleted_")
		}
	}
	if local == "" || local != deleted || local == realID {
		t.Fatalf("local %q, deleted %q", local, deleted)
	}
}
