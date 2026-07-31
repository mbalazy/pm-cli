package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestClaudeLaunchEnv(t *testing.T) {
	custom := t.TempDir() // any dir != DefaultClaudeConfigDir()

	t.Run("default config dir, no extra -> nil (inherit unchanged)", func(t *testing.T) {
		if got := claudeLaunchEnv(""); got != nil {
			t.Fatalf("expected nil env, got %d entries", len(got))
		}
		if got := claudeLaunchEnv(storage.DefaultClaudeConfigDir()); got != nil {
			t.Fatalf("expected nil env for default dir, got %d entries", len(got))
		}
	})

	t.Run("default config dir + extra -> environ + extra appended last", func(t *testing.T) {
		got := claudeLaunchEnv("", "SIM_UDID=2CE9", "ADDITIONAL_METRO_PORT=8090")
		if len(got) != len(os.Environ())+2 {
			t.Fatalf("env len = %d, want environ+2", len(got))
		}
		if got[len(got)-2] != "SIM_UDID=2CE9" || got[len(got)-1] != "ADDITIONAL_METRO_PORT=8090" {
			t.Fatalf("extra pairs not appended last: %v", got[len(got)-2:])
		}
		for _, kv := range got {
			if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") && !envInherited("CLAUDE_CONFIG_DIR") {
				t.Fatalf("default config dir must not be pinned: %s", kv)
			}
		}
	})

	t.Run("custom config dir + extra -> config dir pinned, extra last", func(t *testing.T) {
		got := claudeLaunchEnv(custom, "SIM_UDID=2CE9")
		if got[len(got)-1] != "SIM_UDID=2CE9" {
			t.Fatalf("extra must be last (wins over inherited): %v", got[len(got)-1])
		}
		if got[len(got)-2] != "CLAUDE_CONFIG_DIR="+custom {
			t.Fatalf("config dir not pinned before extra: %v", got[len(got)-2])
		}
	})
}

func envInherited(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

func TestClaudeEnvPrefix(t *testing.T) {
	custom := t.TempDir()

	t.Run("nothing to inject -> empty prefix", func(t *testing.T) {
		if got := claudeEnvPrefix(""); got != "" {
			t.Fatalf("expected empty prefix, got %q", got)
		}
	})

	t.Run("extra only, values shell-quoted, order preserved", func(t *testing.T) {
		got := claudeEnvPrefix("", "SIM_UDID=2CE9", "NAME=a b")
		if got != "SIM_UDID='2CE9' NAME='a b' " {
			t.Fatalf("prefix = %q", got)
		}
	})

	t.Run("config dir precedes extra", func(t *testing.T) {
		got := claudeEnvPrefix(custom, "SIM_UDID=2CE9")
		want := "CLAUDE_CONFIG_DIR=" + shellQuote(custom) + " SIM_UDID='2CE9' "
		if got != want {
			t.Fatalf("prefix = %q, want %q", got, want)
		}
	})

	t.Run("malformed pair without = is dropped", func(t *testing.T) {
		if got := claudeEnvPrefix("", "NOEQUALS"); got != "" {
			t.Fatalf("expected malformed pair dropped, got %q", got)
		}
	})
}

func writeBoardSessionLock(t *testing.T, projDir string, slot, pid int) {
	t.Helper()
	path := storage.SessionLockPath(projDir, slot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"pid":%d,"session_id":"held","slot":%d}`, pid, slot)
	if err := os.WriteFile(path, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPickWorktreeSlot(t *testing.T) {
	newModel := func(t *testing.T) *Model {
		return &Model{store: &storage.Store{Root: t.TempDir()}}
	}
	twoSlots := func() *storage.Project {
		return &storage.Project{
			Path: "/tmp/pick-slot-repo",
			Executor: &storage.Executor{
				Env: map[string]string{"SIM_UDID": "BASE"},
				Worktrees: []storage.WorktreeSlot{
					{Env: map[string]string{"ADDITIONAL_METRO_PORT": "8090"}},
					{Env: map[string]string{"ADDITIONAL_METRO_PORT": "8091"}},
				},
			},
		}
	}
	const deadPID = 2147483646

	t.Run("nil project yields nothing", func(t *testing.T) {
		claim := newModel(t).pickWorktreeSlot(nil, "p", "sess")
		if claim.env != nil || claim.lockCmd != "" {
			t.Fatalf("claim = %+v, want zero", claim)
		}
	})

	t.Run("no slots passes executor env through without a claim", func(t *testing.T) {
		proj := &storage.Project{Executor: &storage.Executor{
			Env: map[string]string{"SIM_UDID": "2CE9", "ADDITIONAL_METRO_PORT": "8090"},
		}}
		claim := newModel(t).pickWorktreeSlot(proj, "p", "sess")
		if len(claim.env) != 2 || claim.env[0] != "ADDITIONAL_METRO_PORT=8090" || claim.env[1] != "SIM_UDID=2CE9" {
			t.Fatalf("env = %v", claim.env)
		}
		if claim.lockCmd != "" {
			t.Fatalf("legacy env passthrough must not claim a slot, got %q", claim.lockCmd)
		}
	})

	t.Run("first free slot is claimed", func(t *testing.T) {
		m := newModel(t)
		claim := m.pickWorktreeSlot(twoSlots(), "p", "sess-1")
		if !containsEnv(claim.env, "ADDITIONAL_METRO_PORT=8090") {
			t.Fatalf("want slot 1 env, got %v", claim.env)
		}
		if !strings.Contains(claim.lockCmd, "slot-1.lock") || !strings.Contains(claim.lockCmd, `"$$"`) {
			t.Fatalf("lockCmd should claim slot 1 with $$, got %q", claim.lockCmd)
		}
	})

	t.Run("live session on slot 1 pushes launch to slot 2", func(t *testing.T) {
		m := newModel(t)
		writeBoardSessionLock(t, m.store.ProjectDir("p"), 1, os.Getpid())
		claim := m.pickWorktreeSlot(twoSlots(), "p", "sess-2")
		if !containsEnv(claim.env, "ADDITIONAL_METRO_PORT=8091") {
			t.Fatalf("want slot 2 env, got %v", claim.env)
		}
		if !strings.Contains(claim.lockCmd, "slot-2.lock") {
			t.Fatalf("lockCmd should claim slot 2, got %q", claim.lockCmd)
		}
	})

	t.Run("stale lock reads as free", func(t *testing.T) {
		m := newModel(t)
		writeBoardSessionLock(t, m.store.ProjectDir("p"), 1, deadPID)
		claim := m.pickWorktreeSlot(twoSlots(), "p", "sess-3")
		if !containsEnv(claim.env, "ADDITIONAL_METRO_PORT=8090") {
			t.Fatalf("stale slot 1 should be reused, got %v", claim.env)
		}
	})

	t.Run("all slots busy degrades to slot 1 env without a claim", func(t *testing.T) {
		m := newModel(t)
		writeBoardSessionLock(t, m.store.ProjectDir("p"), 1, os.Getpid())
		writeBoardSessionLock(t, m.store.ProjectDir("p"), 2, os.Getpid())
		claim := m.pickWorktreeSlot(twoSlots(), "p", "sess-4")
		if !containsEnv(claim.env, "ADDITIONAL_METRO_PORT=8090") {
			t.Fatalf("fallback should carry slot 1 env, got %v", claim.env)
		}
		if claim.lockCmd != "" {
			t.Fatalf("no free slot -> no claim, got %q", claim.lockCmd)
		}
	})
}

func containsEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestSessionLockScript(t *testing.T) {
	got := sessionLockScript("/data/pm/proj", 2, "abcd-1234")
	if !strings.Contains(got, "mkdir -p '/data/pm/proj/.sessions'") {
		t.Errorf("script must create the lock dir, got %q", got)
	}
	if !strings.Contains(got, `"$$"`) || !strings.Contains(got, `"pid":%d`) {
		t.Errorf("script must record the shell pid via printf %%d + $$, got %q", got)
	}
	if !strings.Contains(got, "abcd-1234") || !strings.Contains(got, `"slot":2`) {
		t.Errorf("script must embed session id and slot, got %q", got)
	}
	if !strings.Contains(got, "slot-2.lock") {
		t.Errorf("script must target the slot's lock file, got %q", got)
	}
	if !strings.HasSuffix(got, "; ") {
		t.Errorf("script must end with '; ' so a failed lock write never blocks the launch, got %q", got)
	}
}

// TestSessionLockScriptWritesParseableLock runs the ACTUAL production
// snippet (not a reimplementation) through a real shell and parses the
// result through storage.ReadSessionLock, binding the test to whatever
// format the snippet really emits - renaming a JSON tag here breaks it,
// unlike a fixture built with json.Marshal in the test itself.
func TestSessionLockScriptWritesParseableLock(t *testing.T) {
	dir := t.TempDir()
	script := sessionLockScript(dir, 3, "sess-xyz")

	cmd := exec.Command("sh", "-c", script+"true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\noutput: %s\nscript: %s", err, out, script)
	}

	lk, err := storage.ReadSessionLock(dir, 3)
	if err != nil {
		t.Fatalf("ReadSessionLock: %v", err)
	}
	if lk == nil {
		t.Fatal("expected the lock file to be written")
	}
	if lk.SessionID != "sess-xyz" || lk.Slot != 3 {
		t.Fatalf("lock = %+v, want session_id=sess-xyz slot=3", lk)
	}
	if lk.PID == 0 {
		t.Fatalf("lock pid must be the writing shell's pid, got %+v", lk)
	}
	if lk.Started == "" {
		t.Fatalf("lock started must be set, got %+v", lk)
	}

	sessionsDir := filepath.Dir(storage.SessionLockPath(dir, 3))
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("leftover tmp file after script ran: %s", e.Name())
		}
	}
}
