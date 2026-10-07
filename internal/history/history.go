// Package history records every destructive operation in an append-only
// JSON Lines log (%LOCALAPPDATA%\oow\history.jsonl), so users can always see
// what was changed. Set OOW_NO_OPLOG=1 to disable recording.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileName is the history file inside the data directory.
const FileName = "history.jsonl"

// Record describes one completed (or cancelled) operation.
type Record struct {
	Time       time.Time    `json:"time"`
	Command    string       `json:"command"`
	Sandbox    bool         `json:"sandbox,omitempty"`
	Removed    int          `json:"removed"`
	Reclaimed  int64        `json:"reclaimed_bytes"`
	Skipped    int          `json:"skipped"`
	Errors     int          `json:"errors"`
	Cancelled  bool         `json:"cancelled,omitempty"`
	DurationMS int64        `json:"duration_ms"`
	Targets    []TargetStat `json:"targets,omitempty"`
	// Recycled is the size of items moved to the Recycle Bin (recoverable
	// until it is emptied), as opposed to Reclaimed, which was deleted.
	Recycled int64 `json:"recycled_bytes,omitempty"`
	// Apps lists applications uninstalled in this operation. Later leftover
	// scans use them as evidence of what used to be installed.
	Apps []AppIdentity `json:"apps,omitempty"`
	// Changes lists settings changed by the operation (startup entries,
	// the user PATH, maintenance tasks), each with what is needed to undo it.
	Changes []Change `json:"changes,omitempty"`
}

// Change is one setting changed or one task run.
type Change struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action"` // e.g. disabled, enabled, removed-path-entry, ran
	Status string `json:"status"` // changed, skipped, failed
	// Detail is e.g. the value before the change or the backup file.
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AppIdentity records an uninstalled application.
type AppIdentity struct {
	Name            string   `json:"name"`
	Version         string   `json:"version,omitempty"`
	Publisher       string   `json:"publisher,omitempty"`
	InstallLocation string   `json:"install_location,omitempty"`
	Exes            []string `json:"exes,omitempty"`
}

// TargetStat is the per-target breakdown of a record (a rule, an app, ...).
type TargetStat struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Removed     int           `json:"removed"`
	Reclaimed   int64         `json:"reclaimed_bytes"`
	Skipped     int           `json:"skipped"`
	Errors      int           `json:"errors"`
	SkipReasons []ReasonCount `json:"skip_reasons,omitempty"`
}

// ReasonCount is a skip reason with a count.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Disabled reports whether recording is turned off.
func Disabled() bool { return os.Getenv("OOW_NO_OPLOG") == "1" }

// Append writes one record. It is a no-op when recording is disabled.
func Append(dataDir string, r Record) error {
	if Disabled() {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, FileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	return errors.Join(werr, f.Close())
}

// Load returns up to limit records, newest first (limit <= 0 means all).
// Malformed lines, e.g. from an interrupted write, are skipped.
func Load(dataDir string, limit int) ([]Record, error) {
	f, err := os.Open(filepath.Join(dataDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && !r.Time.IsZero() {
			out = append(out, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
