package storage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Dismissed rows of the attention queue (pm-cli-125): "I will not act on
// this" for a row whose condition otherwise holds forever - a run nobody will
// accept, a sub whose PR will never be opened, a solo report already read.
// Kept in <pm-root>/.cockpit/dismissed.json next to the change feed's cache,
// NOT on the task: it is a statement about one row of the queue, and the
// task stays exactly as it is. The key carries the row's Since stamp, so a
// NEW run on the same tracker (a new stamp) comes back into the queue by
// itself.

// DismissableSections are the sections whose rows carry the dismiss action.
var DismissableSections = []string{SectionNeedsMe, SectionSoloReports, SectionLandedNoPR}

// IsDismissable reports whether a section's rows can be dismissed.
func IsDismissable(section string) bool {
	for _, s := range DismissableSections {
		if s == section {
			return true
		}
	}
	return false
}

// DismissKey identifies one row: section, project, the task or shift id, and
// the stamp the row's condition started at.
func DismissKey(section, project, id, since string) string {
	return section + "|" + project + "|" + id + "|" + since
}

// DismissKey is the row's key in dismissed.json.
func (r AttentionRow) DismissKey() string {
	id := r.TaskID
	if r.Shift != "" {
		id = r.Shift
	}
	return DismissKey(r.Section, r.Project, id, r.Since)
}

type dismissedFile struct {
	// Rows maps a DismissKey to when it was dismissed (RFC3339).
	Rows map[string]string `json:"rows"`
}

// dismissMu serialises the read-modify-write of dismissed.json inside one
// process (pm serve is its only writer).
var dismissMu sync.Mutex

func dismissedPath(root string) string {
	return filepath.Join(root, ".cockpit", "dismissed.json")
}

// ReadDismissed returns the dismissed keys; no file = none.
func ReadDismissed(root string) (map[string]string, error) {
	data, err := os.ReadFile(dismissedPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f dismissedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Rows == nil {
		f.Rows = map[string]string{}
	}
	return f.Rows, nil
}

// AddDismissed records keys as dismissed at now.
func AddDismissed(root string, keys []string, now time.Time) error {
	dismissMu.Lock()
	defer dismissMu.Unlock()
	rows, err := ReadDismissed(root)
	if err != nil {
		return err
	}
	for _, k := range keys {
		rows[k] = now.Format(time.RFC3339)
	}
	return writeDismissed(root, rows)
}

// RemoveDismissed drops every key match accepts and reports how many.
func RemoveDismissed(root string, match func(key string) bool) (int, error) {
	dismissMu.Lock()
	defer dismissMu.Unlock()
	rows, err := ReadDismissed(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for k := range rows {
		if match(k) {
			delete(rows, k)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeDismissed(root, rows)
}

func writeDismissed(root string, rows map[string]string) error {
	path := dismissedPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(dismissedFile{Rows: rows}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o644)
}
