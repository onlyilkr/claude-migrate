// Command anonymize copies a Claude Desktop session store into a test fixture,
// replacing every string that could identify a person, path, or project while
// keeping field names, value types, and nesting.
//
//	go run ./scripts/anonymize -src "<store dir>" -out internal/migrate/testdata/realistic
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// keep lists keys whose string values are safe enums and stay as they are.
var keep = map[string]bool{
	"permissionMode": true, "effort": true, "model": true,
	"chromePermissionMode": true, "titleSource": true, "envScopeId": true,
}

var (
	identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	uuidRe  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type anon struct {
	srcAccount string
	salt       []byte
	seen       map[string]string
	n          int
}

func newAnon(srcAccount string, salt []byte) *anon {
	return &anon{srcAccount: srcAccount, salt: salt, seen: map[string]string{}}
}

// fakeUUID maps a real id to a stable fake one, so cross-file references survive.
// The salt is random per run and never written out, so a fake cannot be tied back
// to a known real id by hashing guesses.
func (a *anon) fakeUUID(s string) string {
	h := sha256.New()
	h.Write(a.salt)
	h.Write([]byte("claude-migrate-fixture:" + s))
	x := hex.EncodeToString(h.Sum(nil)[:16])
	return x[0:8] + "-" + x[8:12] + "-4" + x[13:16] + "-8" + x[17:20] + "-" + x[20:32]
}

func (a *anon) str(key, s string) string {
	if v, ok := a.seen[s]; ok {
		return v
	}
	var v string
	switch {
	case s == "":
		v = ""
	case uuidRe.MatchString(s):
		v = a.fakeUUID(s)
	case strings.HasPrefix(s, "local_") && uuidRe.MatchString(s[len("local_"):]):
		v = "local_" + a.fakeUUID(s[len("local_"):])
	case strings.Contains(s, "/scratch-workspaces/"+a.srcAccount+"/"):
		a.n++
		v = fmt.Sprintf("{{SCRATCH}}/{{SRC_ACCOUNT}}/x/scratch-%d", a.n)
	case strings.HasPrefix(s, "/"):
		a.n++
		v = fmt.Sprintf("/Users/dev/path-%d", a.n)
	default:
		a.n++
		v = fmt.Sprintf("%s-%d", key, a.n)
	}
	a.seen[s] = v
	return v
}

// value anonymizes v, found under key. Every map key below the top level is data
// (paths, ids, names), so it is renamed; values are named after the renamed key,
// never the original.
func (a *anon) value(key string, v any) any {
	switch x := v.(type) {
	case string:
		if keep[key] {
			return x
		}
		return a.str(key, x)
	case []any:
		for i := range x {
			x[i] = a.value(key, x[i])
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			nk := a.str("key", k)
			out[nk] = a.value(nk, vv)
		}
		return out
	default:
		return x // json.Number, bool, nil
	}
}

// schemaKey keeps a record's own field name (the schema), unless it is not a
// plain identifier, in which case it is renamed like any other key.
func (a *anon) schemaKey(k string) string {
	if identRe.MatchString(k) {
		return k
	}
	return a.str("key", k)
}

// top anonymizes one file's top-level JSON value. The top-level keys of a record
// are the schema and stay. scheduled-tasks.json also keeps the keys of each task
// (the tool reads id and displayName there); everything deeper is renamed.
func (a *anon) top(name string, doc any) any {
	m, ok := doc.(map[string]any)
	if !ok {
		return a.value("", doc)
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		nk := a.schemaKey(k)
		if tasks, ok := v.([]any); ok && name == "scheduled-tasks.json" && k == "scheduledTasks" {
			out[nk] = a.tasks(nk, tasks)
			continue
		}
		out[nk] = a.value(nk, v)
	}
	return out
}

func (a *anon) tasks(key string, tasks []any) []any {
	out := make([]any, len(tasks))
	for i, t := range tasks {
		m, ok := t.(map[string]any)
		if !ok {
			out[i] = a.value(key, t)
			continue
		}
		task := make(map[string]any, len(m))
		for k, v := range m {
			nk := a.schemaKey(k)
			task[nk] = a.value(nk, v)
		}
		out[i] = task
	}
	return out
}

func run(a *anon, src, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "deleted_"):
			id := strings.TrimSuffix(strings.TrimPrefix(name, "deleted_"), ".json")
			if err := os.WriteFile(filepath.Join(out, "deleted_"+a.fakeUUID(id)), nil, 0o644); err != nil {
				return err
			}
		case (strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json")) || name == "scheduled-tasks.json":
			b, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				return err
			}
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.UseNumber()
			var doc any
			if err := dec.Decode(&doc); err != nil {
				fmt.Fprintf(os.Stderr, "skip unreadable %s: %v\n", name, err)
				continue
			}
			outName := name
			if strings.HasPrefix(name, "local_") {
				outName = "local_" + a.fakeUUID(strings.TrimSuffix(strings.TrimPrefix(name, "local_"), ".json")) + ".json"
			}
			enc, err := json.MarshalIndent(a.top(name, doc), "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, outName), append(enc, '\n'), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	src := flag.String("src", "", "store directory <sessions root>/<account>/<org>")
	out := flag.String("out", "", "fixture output directory")
	flag.Parse()
	if *src == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	a := newAnon(filepath.Base(filepath.Dir(filepath.Clean(*src))), salt)
	if err := run(a, *src, *out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
