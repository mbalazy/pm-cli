package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SyncStore wraps a TaskStore and enqueues sync events on every write.
// A background goroutine flushes the queue to a remote pm-sync API.
// Opt-in: created only when PM_SYNC_URL is set.
type SyncStore struct {
	TaskStore // embedded for read-through

	queuePath string // .sync-queue.jsonl
	mu        sync.Mutex
	client    *syncClient
	done      chan struct{}
	wg        sync.WaitGroup
}

// SyncOp represents a single queued sync operation.
type SyncOp struct {
	Op      string `json:"op"`      // "upsert" or "delete"
	File    string `json:"file"`    // relative path within pm root (e.g. "project/task-1-foo.md")
	Version int    `json:"version"` // monotonic per-file, for conflict detection
	TS      string `json:"ts"`      // RFC3339
}

// NewSyncStore wraps a TaskStore with sync capabilities.
// url is the pm-sync API base URL, token is the auth token.
func NewSyncStore(inner TaskStore, url, token string) *SyncStore {
	ss := &SyncStore{
		TaskStore: inner,
		queuePath: filepath.Join(inner.RootDir(), ".sync-queue.jsonl"),
		client:    newSyncClient(url, token),
		done:      make(chan struct{}),
	}
	ss.wg.Add(1)
	go ss.flushLoop()
	return ss
}

// Close stops the background flush loop and waits for it to finish.
func (ss *SyncStore) Close() {
	close(ss.done)
	ss.wg.Wait()
}

// Flush drains the queue synchronously. Used by `pm sync` command.
func (ss *SyncStore) Flush() error {
	return ss.drainQueue()
}

// QueueLen returns the number of pending sync operations.
func (ss *SyncStore) QueueLen() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ops, _ := ss.readQueue()
	return len(ops)
}

// --- Write methods (override embedded TaskStore) ---

func (ss *SyncStore) AddTask(projectSlug string, t *Task) error {
	if err := ss.TaskStore.AddTask(projectSlug, t); err != nil {
		return err
	}
	ss.enqueue("upsert", t.FilePath)
	return nil
}

func (ss *SyncStore) WriteTask(t *Task) error {
	if err := ss.TaskStore.WriteTask(t); err != nil {
		return err
	}
	ss.enqueue("upsert", t.FilePath)
	return nil
}

func (ss *SyncStore) MoveTask(t *Task, newStatus TaskStatus) error {
	if err := ss.TaskStore.MoveTask(t, newStatus); err != nil {
		return err
	}
	ss.enqueue("upsert", t.FilePath)
	return nil
}

func (ss *SyncStore) DeleteTask(t *Task) error {
	filePath := t.FilePath
	if err := ss.TaskStore.DeleteTask(t); err != nil {
		return err
	}
	ss.enqueue("delete", filePath)
	return nil
}

func (ss *SyncStore) CreateProject(slug string, p *Project) error {
	if err := ss.TaskStore.CreateProject(slug, p); err != nil {
		return err
	}
	ss.enqueue("upsert", ss.TaskStore.ProjectYAML(slug))
	return nil
}

func (ss *SyncStore) UpdateProject(slug string, p *Project) error {
	if err := ss.TaskStore.UpdateProject(slug, p); err != nil {
		return err
	}
	ss.enqueue("upsert", ss.TaskStore.ProjectYAML(slug))
	return nil
}

// --- Queue operations ---

func (ss *SyncStore) enqueue(op, absPath string) {
	rel, err := filepath.Rel(ss.TaskStore.RootDir(), absPath)
	if err != nil {
		rel = absPath // fallback
	}

	entry := SyncOp{
		Op:   op,
		File: rel,
		TS:   time.Now().UTC().Format(time.RFC3339),
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	f, err := os.OpenFile(ss.queuePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // best-effort, don't block writes
	}
	defer f.Close()
	json.NewEncoder(f).Encode(entry)
}

func (ss *SyncStore) readQueue() ([]SyncOp, error) {
	f, err := os.Open(ss.queuePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var ops []SyncOp
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var op SyncOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			continue // skip malformed lines
		}
		ops = append(ops, op)
	}
	return ops, scanner.Err()
}

func (ss *SyncStore) clearQueue() error {
	return os.Remove(ss.queuePath)
}

// rewriteQueue writes remaining ops back to the queue file.
func (ss *SyncStore) rewriteQueue(ops []SyncOp) error {
	if len(ops) == 0 {
		return ss.clearQueue()
	}
	f, err := os.Create(ss.queuePath)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, op := range ops {
		if err := enc.Encode(op); err != nil {
			return err
		}
	}
	return nil
}

// --- Flush logic ---

func (ss *SyncStore) flushLoop() {
	defer ss.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ss.done:
			// Final flush on shutdown
			ss.drainQueue()
			return
		case <-ticker.C:
			ss.drainQueue()
		}
	}
}

func (ss *SyncStore) drainQueue() error {
	ss.mu.Lock()
	ops, err := ss.readQueue()
	if err != nil || len(ops) == 0 {
		ss.mu.Unlock()
		return err
	}
	// Clear queue while holding lock - we'll rewrite failures
	ss.clearQueue()
	ss.mu.Unlock()

	// Deduplicate: keep last op per file
	deduped := deduplicateOps(ops)

	var failed []SyncOp
	for _, op := range deduped {
		absPath := filepath.Join(ss.TaskStore.RootDir(), op.File)
		var sendErr error
		switch op.Op {
		case "upsert":
			data, readErr := os.ReadFile(absPath)
			if readErr != nil {
				// File was deleted between enqueue and flush - skip
				continue
			}
			sendErr = ss.client.Upsert(op.File, data)
		case "delete":
			sendErr = ss.client.Delete(op.File)
		}
		if sendErr != nil {
			failed = append(failed, op)
		}
	}

	// Re-enqueue failed ops
	if len(failed) > 0 {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		// Merge with any new ops that arrived during flush
		newOps, _ := ss.readQueue()
		all := append(failed, newOps...)
		return ss.rewriteQueue(all)
	}
	return nil
}

// deduplicateOps keeps only the last operation per file.
func deduplicateOps(ops []SyncOp) []SyncOp {
	seen := make(map[string]int) // file -> index in result
	var result []SyncOp
	for _, op := range ops {
		if idx, ok := seen[op.File]; ok {
			result[idx] = op // replace with later op
		} else {
			seen[op.File] = len(result)
			result = append(result, op)
		}
	}
	return result
}

// --- HTTP client ---

type syncClient struct {
	baseURL string
	token   string
}

func newSyncClient(baseURL, token string) *syncClient {
	return &syncClient{baseURL: strings.TrimRight(baseURL, "/"), token: token}
}

func (c *syncClient) Upsert(relPath string, data []byte) error {
	// TODO: implement HTTP PUT to pm-sync API
	// PUT {baseURL}/files/{relPath}
	// Headers: Authorization: Bearer {token}, Content-Type: application/octet-stream
	// Body: raw file content
	// On 409: field-level merge (links union, brief LWW, body concat, etc.)
	_ = relPath
	_ = data
	return fmt.Errorf("sync client not yet implemented")
}

func (c *syncClient) Delete(relPath string) error {
	// TODO: implement HTTP DELETE to pm-sync API
	// DELETE {baseURL}/files/{relPath}
	// Headers: Authorization: Bearer {token}
	_ = relPath
	return fmt.Errorf("sync client not yet implemented")
}
