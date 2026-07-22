package board

import (
	"os"
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

func TestWorktreeLaunchEnv(t *testing.T) {
	if got := worktreeLaunchEnv(nil); got != nil {
		t.Fatalf("nil project should yield nil, got %v", got)
	}
	if got := worktreeLaunchEnv(&storage.Project{}); got != nil {
		t.Fatalf("project without executor env should yield nil, got %v", got)
	}
	proj := &storage.Project{Executor: &storage.Executor{
		Env: map[string]string{"SIM_UDID": "2CE9", "ADDITIONAL_METRO_PORT": "8090"},
	}}
	got := worktreeLaunchEnv(proj)
	if len(got) != 2 || got[0] != "ADDITIONAL_METRO_PORT=8090" || got[1] != "SIM_UDID=2CE9" {
		t.Fatalf("worktreeLaunchEnv = %v", got)
	}
	// executor.env flows into worktree launches even without additional_worktree
	// (unconditional passthrough - the capability flag gates only --additional).
	if proj.Executor.AdditionalWorktree {
		t.Fatal("test project must not have additional_worktree set")
	}
}
