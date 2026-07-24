package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSessionLockFile(t *testing.T, projDir string, lk SessionLock) {
	t.Helper()
	path := SessionLockPath(projDir, lk.Slot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(lk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLock(t *testing.T) {
	const deadPID = 2147483646

	t.Run("unclaimed slot reads as free", func(t *testing.T) {
		dir := t.TempDir()
		lk, err := ReadSessionLock(dir, 1)
		if err != nil || lk != nil {
			t.Fatalf("ReadSessionLock = %v, %v; want nil, nil", lk, err)
		}
		if LiveSessionHolder(dir, 1) != nil {
			t.Fatal("unclaimed slot must have no live holder")
		}
	})

	t.Run("live holder is reported", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionLockFile(t, dir, SessionLock{PID: os.Getpid(), SessionID: "s-1", Slot: 2})
		got := LiveSessionHolder(dir, 2)
		if got == nil || got.SessionID != "s-1" || got.PID != os.Getpid() {
			t.Fatalf("LiveSessionHolder = %+v", got)
		}
	})

	t.Run("stale lock with dead pid reads as free", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionLockFile(t, dir, SessionLock{PID: deadPID, SessionID: "s-2", Slot: 1})
		if LiveSessionHolder(dir, 1) != nil {
			t.Fatal("dead holder must read as free")
		}
	})

	t.Run("corrupt lock reads as free", func(t *testing.T) {
		dir := t.TempDir()
		path := SessionLockPath(dir, 1)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte("{truncated"), 0644)
		if LiveSessionHolder(dir, 1) != nil {
			t.Fatal("corrupt lock must not brick the slot")
		}
	})

	t.Run("slots are independent", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionLockFile(t, dir, SessionLock{PID: os.Getpid(), SessionID: "s-1", Slot: 1})
		if LiveSessionHolder(dir, 1) == nil {
			t.Fatal("slot 1 should be held")
		}
		if LiveSessionHolder(dir, 2) != nil {
			t.Fatal("slot 2 should be free")
		}
	})
}
