// Package manifest records every file an apply run creates, so undo can remove
// exactly those files and nothing else.
package manifest

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var ErrNoRuns = errors.New("no migration runs found in ~/.claude-migrate/runs")

type Header struct {
	ToolVersion string    `json:"tool_version"`
	Source      string    `json:"source"`
	Target      string    `json:"target"`
	TargetDir   string    `json:"target_dir"`
	StartedAt   time.Time `json:"started_at"`
}

type Entry struct {
	Path         string `json:"path"`
	SourceFile   string `json:"source_file"`
	CLISessionID string `json:"cli_session_id"`
	SHA256       string `json:"sha256"`
	// Retracted marks a follow-up entry for a path whose write failed: the file
	// there, if any, is not this run's, so undo must leave it alone.
	Retracted bool `json:"retracted,omitempty"`
}

type Writer struct {
	Path string
	f    *os.File
}

// Create starts a new run file named after h.StartedAt. It never overwrites.
func Create(runsDir string, h Header) (*Writer, error) {
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(runsDir, h.StartedAt.UTC().Format("20060102T150405.000Z")+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	w := &Writer{Path: path, f: f}
	if err := w.writeLine(h); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Add appends one entry and fsyncs, so the entry survives a crash right after.
func (w *Writer) Add(e Entry) error { return w.writeLine(e) }

func (w *Writer) Close() error { return w.f.Close() }

func (w *Writer) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.f.Sync()
}

// Read parses a run file. A malformed final line (a write cut short by a crash)
// is ignored; a malformed line anywhere else is an error.
func Read(path string) (Header, []Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Header{}, nil, err
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		return Header{}, nil, fmt.Errorf("%s: missing header", path)
	}
	var h Header
	if err := json.Unmarshal(lines[0], &h); err != nil {
		return Header{}, nil, fmt.Errorf("%s: header: %w", path, err)
	}
	var entries []Entry
	for i, line := range lines[1:] {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			if i == len(lines)-2 {
				break
			}
			return Header{}, nil, fmt.Errorf("%s: line %d: %w", path, i+2, err)
		}
		entries = append(entries, e)
	}
	return h, entries, nil
}

// readableHeader reports whether the run file's first line parses as a Header
// that names a target and a target directory ({} and null do not).
func readableHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadBytes('\n')
	var h Header
	return json.Unmarshal(bytes.TrimSpace(line), &h) == nil && h.Target != "" && h.TargetDir != ""
}

// Latest returns the newest run that has not been undone and whose header is
// readable. Newer runs with an unreadable header are passed over and listed in
// skipped (newest first) so the caller can say so. A run with a good header but
// a damaged body is not skipped: undoing it must report the damage.
func Latest(runsDir string) (path string, skipped []string, err error) {
	matches, err := filepath.Glob(filepath.Join(runsDir, "*.jsonl"))
	if err != nil {
		return "", nil, err
	}
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		if readableHeader(matches[i]) {
			return matches[i], skipped, nil
		}
		skipped = append(skipped, matches[i])
	}
	return "", skipped, ErrNoRuns
}

func HashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
