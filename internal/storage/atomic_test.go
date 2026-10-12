package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// onlyEntries fails the test when dir holds anything but the named entries -
// a scratch file left next to the target is exactly what atomicWriteFile
// must never do.
func onlyEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	for _, w := range want {
		keep[w] = true
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			t.Errorf("unexpected entry left behind: %s", e.Name())
		}
	}
}

func TestAtomicWriteFile(t *testing.T) {
	t.Run("success replaces the target and leaves no scratch", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "f.json")
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := atomicWriteFile(path, []byte("new"), 0644); err != nil {
			t.Fatalf("atomicWriteFile: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "new" {
			t.Fatalf("target = %q, %v; want new", data, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0644 {
			t.Errorf("perm = %v, want 0644", info.Mode().Perm())
		}
		onlyEntries(t, dir, "f.json")
	})

	t.Run("write failure leaves the target untouched and no scratch", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a directory the test cannot write into")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "f.json")
		if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
		if err := atomicWriteFile(path, []byte("new"), 0644); err == nil {
			t.Fatal("atomicWriteFile into a read-only directory should fail")
		}
		if data, _ := os.ReadFile(path); string(data) != "old" {
			t.Errorf("target = %q, want old", data)
		}
		onlyEntries(t, dir, "f.json")
	})

	t.Run("rename failure removes the scratch and leaves the target untouched", func(t *testing.T) {
		dir := t.TempDir()
		// A non-empty directory where the file goes: rename(2) cannot
		// replace it, so the write succeeds and only the publish fails.
		path := filepath.Join(dir, "f.json")
		if err := os.MkdirAll(filepath.Join(path, "keep"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := atomicWriteFile(path, []byte("new"), 0644); err == nil {
			t.Fatal("atomicWriteFile over a non-empty directory should fail")
		}
		if info, err := os.Stat(filepath.Join(path, "keep")); err != nil || !info.IsDir() {
			t.Errorf("target directory was disturbed: %v", err)
		}
		onlyEntries(t, dir, "f.json")
	})

	t.Run("missing parent directory fails without creating anything", func(t *testing.T) {
		dir := t.TempDir()
		if err := atomicWriteFile(filepath.Join(dir, "gone", "f.json"), []byte("new"), 0644); err == nil {
			t.Fatal("atomicWriteFile under a missing directory should fail")
		}
		onlyEntries(t, dir)
	})
}

// Two scratch names for the same target never coincide within a process -
// the property every claim, lock and overwrite site relies on.
func TestScratchPathIsUniquePerCall(t *testing.T) {
	a, b := scratchPath("/x/lock", "tmp"), scratchPath("/x/lock", "tmp")
	if a == b {
		t.Fatalf("scratchPath returned %q twice", a)
	}
	if filepath.Dir(a) != "/x" {
		t.Errorf("scratch %q is not next to its target", a)
	}
}

// A create loop killed between link(2) and removing its scratch leaves
// `<path>.tmp.<pid>.<seq>` as a second name of the LIVE lock or claim. An
// overwrite must never compute that name: truncating it would rewrite the
// live file in place, the torn window the overwrite exists to avoid.
func TestAtomicWriteFileNeverReusesALinkCreateScratch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.finish.claim")
	if err := os.WriteFile(path, []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}
	next := scratchSeq.Load() + 1
	for i := uint64(0); i < 4; i++ {
		leftover := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), next+i)
		if err := os.Link(path, leftover); err != nil {
			t.Fatal(err)
		}
	}
	if err := atomicWriteFile(path, []byte("refreshed"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	for i := uint64(0); i < 4; i++ {
		leftover := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), next+i)
		if data, _ := os.ReadFile(leftover); string(data) != "live" {
			t.Errorf("%s = %q: the overwrite wrote through a link to the old file", filepath.Base(leftover), data)
		}
	}
}
