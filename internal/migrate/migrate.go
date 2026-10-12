// Package migrate turns planned records into new files in the target store.
package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/onlyilkr/claude-migrate/internal/manifest"
	"github.com/onlyilkr/claude-migrate/internal/plan"
)

// ChangedFields describes, for reports, every field Transform touches.
var ChangedFields = []string{"bridgeSessionIds (emptied)", "scheduledTaskId (removed)"}

// Transform returns a copy of raw with account-bound fields reset. Every other
// field, sessionId included, keeps its original JSON value: Desktop keeps one
// pin list for all accounts, keyed by session ID, so a kept ID keeps the pin.
func Transform(raw map[string]json.RawMessage) map[string]json.RawMessage {
	out := maps.Clone(raw)
	out["bridgeSessionIds"] = json.RawMessage(`[]`)
	delete(out, "scheduledTaskId")
	return out
}

// Encode serializes a record without HTML-escaping, two-space indented.
func Encode(rec map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteAtomic writes data to dir/name via a uniquely named temp file that is
// then hard-linked into place. link(2) fails if the name exists, so an existing
// file is never replaced, even by a racing writer. The directory is fsynced.
func WriteAtomic(dir, name string, data []byte) error {
	final := filepath.Join(dir, name)
	f, err := os.CreateTemp(dir, ".claude-migrate-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp, final); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("refusing to overwrite existing %s", final)
		}
		return err
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrPublishedUnsynced, final, err)
	}
	return nil
}

// ErrPublishedUnsynced means the record was linked into place (it exists and is
// this run's) but the directory fsync failed afterwards.
var ErrPublishedUnsynced = errors.New("record written but its directory could not be synced")

// syncDir fsyncs a directory; a variable so tests can make it fail.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Apply writes every planned record under its source file name. Each manifest
// entry is recorded before its file is written, so a crash can never leave an
// unrecorded file behind. A name that is taken by now is refused before it is
// recorded: undo must never be able to remove a file this run did not create.
// It returns how many files were written.
func Apply(pl plan.Plan, w *manifest.Writer) (int, error) {
	for i, r := range pl.Copy {
		data, err := Encode(Transform(r.Raw))
		if err != nil {
			return i, fmt.Errorf("encode %s: %w", r.File, err)
		}
		path := filepath.Join(pl.TargetDir, r.File)
		if _, err := os.Lstat(path); err == nil {
			return i, fmt.Errorf("write %s: refusing to overwrite existing file", path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return i, fmt.Errorf("write %s: %w", path, err)
		}
		if err := w.Add(manifest.Entry{Path: path, SourceFile: r.File, CLISessionID: r.CLISessionID, SHA256: manifest.HashHex(data)}); err != nil {
			return i, fmt.Errorf("manifest: %w", err)
		}
		if err := WriteAtomic(pl.TargetDir, r.File, data); err != nil {
			if errors.Is(err, ErrPublishedUnsynced) {
				return i + 1, fmt.Errorf("write %s: %w", path, err) // the file is ours: keep its entry
			}
			if rerr := w.Add(manifest.Entry{Path: path, SourceFile: r.File, CLISessionID: r.CLISessionID, Retracted: true}); rerr != nil {
				return i, fmt.Errorf("write %s: %w (and retracting its manifest entry failed: %v)", path, err, rerr)
			}
			return i, fmt.Errorf("write %s: %w", path, err)
		}
	}
	return len(pl.Copy), nil
}
