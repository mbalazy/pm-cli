package board

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestClaudeSessionArgs covers pm-cli-72-2 point 6: launchClaude's kinds
// "here"/"tmux" (plus the project-scope launch) share this argv assembly -
// extracted to a pure function so it is unit-testable without spawning
// `claude`, matching the executorPMArgs/codexArgs pattern already in the
// package.
func TestClaudeSessionArgs(t *testing.T) {
	t.Run("no skip flag", func(t *testing.T) {
		got := claudeSessionArgs("sess-1", "", "hello")
		want := []string{"--session-id", "sess-1", "hello"}
		assertArgsEqual(t, got, want)
	})

	t.Run("with skip flag", func(t *testing.T) {
		got := claudeSessionArgs("sess-1", "--dangerously-skip-permissions", "hello")
		want := []string{"--session-id", "sess-1", "--dangerously-skip-permissions", "hello"}
		assertArgsEqual(t, got, want)
	})
}

func TestClaudeWorktreeArgs(t *testing.T) {
	t.Run("no skip flag", func(t *testing.T) {
		got := claudeWorktreeArgs("feat/foo", "sess-1", "", "hello")
		want := []string{"-w", "feat/foo", "--session-id", "sess-1", "hello"}
		assertArgsEqual(t, got, want)
	})

	t.Run("with skip flag", func(t *testing.T) {
		got := claudeWorktreeArgs("feat/foo", "sess-1", "--dangerously-skip-permissions", "hello")
		want := []string{"-w", "feat/foo", "--session-id", "sess-1", "--dangerously-skip-permissions", "hello"}
		assertArgsEqual(t, got, want)
	})
}

func TestClaudeResumeArgs(t *testing.T) {
	t.Run("no skip flag", func(t *testing.T) {
		got := claudeResumeArgs("sess-1", "")
		want := []string{"--resume", "sess-1"}
		assertArgsEqual(t, got, want)
	})

	t.Run("with skip flag", func(t *testing.T) {
		got := claudeResumeArgs("sess-1", "--dangerously-skip-permissions")
		want := []string{"--resume", "sess-1", "--dangerously-skip-permissions"}
		assertArgsEqual(t, got, want)
	})
}

func TestClaudeForkArgs(t *testing.T) {
	t.Run("no skip flag", func(t *testing.T) {
		got := claudeForkArgs("parent-1", "new-1", "")
		want := []string{"--resume", "parent-1", "--fork-session", "--session-id", "new-1"}
		assertArgsEqual(t, got, want)
	})

	t.Run("with skip flag", func(t *testing.T) {
		got := claudeForkArgs("parent-1", "new-1", "--dangerously-skip-permissions")
		want := []string{"--resume", "parent-1", "--fork-session", "--session-id", "new-1", "--dangerously-skip-permissions"}
		assertArgsEqual(t, got, want)
	})
}

func TestClaudeShellCommand(t *testing.T) {
	t.Run("quotes every token, prompt with special chars", func(t *testing.T) {
		got := claudeShellCommand(claudeSessionArgs("sess-1", "", "it's a prompt"))
		want := "claude '--session-id' 'sess-1' 'it'\\''s a prompt'"
		if got != want {
			t.Errorf("claudeShellCommand = %q, want %q", got, want)
		}
	})

	t.Run("worktree args are quoted, including the branch name", func(t *testing.T) {
		got := claudeShellCommand(claudeWorktreeArgs("feat/a b", "sess-1", "--dangerously-skip-permissions", "hi"))
		want := "claude '-w' 'feat/a b' '--session-id' 'sess-1' '--dangerously-skip-permissions' 'hi'"
		if got != want {
			t.Errorf("claudeShellCommand = %q, want %q", got, want)
		}
	})
}

// TestWorktreeLaunchSessionSavedOnlyAfterSetupSucceeds covers pm-cli-132-6
// (pm-cli-77): a failed worktree setup must not leave a session ID on the
// task that no process ever used - saveSession must run AFTER
// copyWorktreeFiles succeeds, not before.
func TestWorktreeLaunchSessionSavedOnlyAfterSetupSucceeds(t *testing.T) {
	for _, kind := range []string{"worktree", "worktree-tmux"} {
		t.Run(kind, func(t *testing.T) {
			store := &storage.Store{Root: t.TempDir()}
			if err := store.CreateProject("p", &storage.Project{
				Name: "P",
				Path: t.TempDir(), // not a git repo -> `git worktree add` fails
			}); err != nil {
				t.Fatal(err)
			}
			task := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Existing", Status: storage.StatusTodo}}
			if err := store.AddTask("p", task); err != nil {
				t.Fatal(err)
			}

			m := Model{
				store:       store,
				projects:    []string{"all", "p"},
				currentView: viewDetail,
				detailState: detailState{detailTask: task},
				width:       80, height: 24,
			}

			result, _ := m.launchClaude(kind)
			m = result.(Model)

			if !strings.Contains(m.toastMsg, "worktree setup failed") {
				t.Errorf("toastMsg = %q, want it to mention the worktree setup failure", m.toastMsg)
			}

			fresh, err := store.FindTask("p", "p-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(fresh.Meta.Sessions) != 0 {
				t.Errorf("Meta.Sessions = %v, want empty - a failed worktree setup must not save a session", fresh.Meta.Sessions)
			}
		})
	}
}

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %v, want %v", got, want)
	}
}
