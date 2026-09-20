package board

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// The Claude/Codex overlay's layout contract. The two questions this menu
// answers are WHERE a session runs and WHICH session it joins; before, both were
// spread across seven variant names ("Worktree + tmux", "Resume in tmux") that
// each had to be read to be told apart.

const testSession = "3f5742db-1111-2222-3333-444455556666"

func llmModel(t *testing.T) *Model {
	t.Helper()
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{
		ID: "p-9", Title: "A task", Status: storage.StatusDoing,
		Branch: "feat/thing", Sessions: []string{testSession},
	}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	m.width, m.height = 130, 40 // room for the full layout
	return m
}

// menuPreview reads the previewed command out of a rendered overlay. The block
// WRAPS onto continuation lines (and is padded with blanks to a fixed height),
// so the command is the arrow line plus every non-empty line under it.
func menuPreview(m *Model) string {
	var parts []string
	started := false
	for _, line := range strings.Split(stripANSI(m.viewClaudeMenu()), "\n") {
		clean := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│"))
		if !started {
			if _, cmd, ok := strings.Cut(clean, "→ "); ok {
				started, parts = true, append(parts, strings.TrimSpace(cmd))
			}
			continue
		}
		if clean == "" {
			break
		}
		parts = append(parts, clean)
	}
	return strings.Join(parts, " ")
}

// One group per PLACE a session can run, each naming the actual directory,
// branch or session rather than the word for it.
func TestTheLLMMenuGroupsByWhereTheSessionRuns(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := llmModel(t)
	m.openClaudeMenu(m.taskByID("p-9"))
	out := stripANSI(m.viewClaudeMenu())

	for _, want := range []string{"IN THE REPO", "ISOLATED WORKTREE   feat/thing", "RESUME   3f5742db"} {
		if !strings.Contains(out, want) {
			t.Errorf("the menu is missing the group %q:\n%s", want, out)
		}
	}
	// Ordered by distance from the checkout you are standing in.
	repo, wt, res := strings.Index(out, "IN THE REPO"), strings.Index(out, "ISOLATED WORKTREE"), strings.Index(out, "RESUME")
	if !(repo < wt && wt < res) {
		t.Errorf("groups out of order (repo %d, worktree %d, resume %d):\n%s", repo, wt, res, out)
	}
	// The agent is named in the title, not on a row of its own.
	if !strings.Contains(out, "Launch Claude Code") {
		t.Errorf("the title does not name the agent:\n%s", out)
	}
}

// The preview names what cannot be known yet rather than inventing it: a
// session id is minted at launch time, and the prompt is a page of context.
func TestTheLLMPreviewTracksTheCursorAndThePermsToggle(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := llmModel(t)
	m.openClaudeMenu(m.taskByID("p-9"))

	if got := menuPreview(m); got != "claude --session-id <new session> <task prompt>" {
		t.Errorf("preview in the repo = %q", got)
	}

	byKind := func(m *Model, kind string) {
		for i, item := range m.claudeMenuItems {
			if item.kind == kind {
				m.claudeMenuCursor = i
			}
		}
	}
	byKind(m, "worktree")
	if got := menuPreview(m); got != "claude -w feat/thing --session-id <new session> <task prompt>" {
		t.Errorf("preview in a worktree = %q", got)
	}
	byKind(m, "resume")
	if got := menuPreview(m); got != "claude --resume "+testSession {
		t.Errorf("preview of a resume = %q", got)
	}

	// The one toggle shows up in the command, which is the only place its
	// effect is concrete.
	next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	v := next.(Model)
	if got := menuPreview(&v); !strings.HasSuffix(got, "--dangerously-skip-permissions") {
		t.Errorf("preview after ! = %q", got)
	}

	// Codex is a different binary with a different flag, and the menu says so
	// rather than leaving the reader to remember which agent is selected.
	v.launchAgent = launchAgentCodex
	v.rebuildClaudeMenuItems(v.menuTask())
	out := stripANSI(v.viewClaudeMenu())
	if !strings.Contains(out, "Launch Codex") || !strings.Contains(out, "bypass sandbox") {
		t.Errorf("the codex menu does not name codex:\n%s", out)
	}
	if got := menuPreview(&v); got != "codex --dangerously-bypass-approvals-and-sandbox <task prompt>" {
		t.Errorf("codex preview = %q", got)
	}
}

// Codex has no resume, and the menu is rebuilt on the agent switch - so the
// group simply is not there rather than being offered and failing.
func TestTheCodexMenuHasNoResumeGroup(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := llmModel(t)
	m.openClaudeMenu(m.taskByID("p-9"))
	next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	v := next.(Model)

	out := stripANSI(v.viewClaudeMenu())
	if strings.Contains(out, "RESUME") {
		t.Errorf("codex cannot resume:\n%s", out)
	}
	if !strings.Contains(out, "ISOLATED WORKTREE") {
		t.Errorf("codex still runs in a worktree:\n%s", out)
	}
}

// A task with no session yet has no resume group and no session in the preview
// - the group's whole subtitle would be a placeholder.
func TestATaskWithNoSessionGetsNoResumeGroup(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Fresh", Status: storage.StatusTodo}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-1"))
	m.width, m.height = 130, 40
	m.openClaudeMenu(m.taskByID("p-1"))

	if out := stripANSI(m.viewClaudeMenu()); strings.Contains(out, "RESUME") {
		t.Errorf("there is no session to resume:\n%s", out)
	}
}

// A short terminal gets a layout that FITS in both directions. lipgloss.Place
// centres the box, so an overlay two lines too tall loses a line off each end -
// and the bottom one is the help, which is where the way out is written.
func TestAShortTerminalStillFitsTheWholeMenu(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := llmModel(t)
	m.openClaudeMenu(m.taskByID("p-9"))
	m.width, m.height = 80, 24

	out := stripANSI(m.viewClaudeMenu())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > m.height {
		t.Fatalf("%d lines in a %d-row terminal:\n%s", len(lines), m.height, out)
	}
	for _, line := range lines {
		if w := len([]rune(strings.TrimRight(line, " "))); w > m.width {
			t.Fatalf("a %d-cell line in an %d-column terminal:\n%s", w, m.width, out)
		}
	}
	// The groups and every key survive; only the hints go.
	for _, want := range []string{"IN THE REPO", "ISOLATED WORKTREE", "RESUME", "h  here", "W  tmux window", "→ claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact dropped %q, which is not a hint:\n%s", want, out)
		}
	}
	if strings.Contains(out, "takes over this terminal") {
		t.Errorf("compact must drop the hints - they are what does not fit:\n%s", out)
	}
}

// The box must not resize as the cursor moves. A worktree name is a slugified
// task title, so moving from "here" to the worktree row could double the
// longest line and shove the whole modal wider under the hand - which is harder
// to read than having no preview at all, because the eye has to re-find every
// row. Width AND height are measured, since the preview also wraps.
func TestTheBoxDoesNotResizeAsTheCursorMoves(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{
		ID: "p-9", Status: storage.StatusDoing, Sessions: []string{testSession},
		// The case from the report: a long slug makes the worktree row's
		// command far longer than the plain one's.
		Title: "pm executor generic engine for executing parent subtask",
	}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	m.width, m.height = 200, 50
	m.openClaudeMenu(m.taskByID("p-9"))

	var wantW, wantH int
	for i := range m.claudeMenuItems {
		m.claudeMenuCursor = i
		out := stripANSI(m.viewClaudeMenu())
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		w := 0
		for _, l := range lines {
			if x := len([]rune(strings.TrimRight(l, " "))); x > w {
				w = x
			}
		}
		if i == 0 {
			wantW, wantH = w, len(lines)
			continue
		}
		if w != wantW {
			t.Errorf("item %d (%s) changed the box width: %d, want %d\n%s",
				i, m.claudeMenuItems[i].kind, w, wantW, out)
		}
		if len(lines) != wantH {
			t.Errorf("item %d (%s) changed the box height: %d, want %d",
				i, m.claudeMenuItems[i].kind, len(lines), wantH)
		}
	}

	// And the long command is WRAPPED rather than cut: every token survives.
	for i, item := range m.claudeMenuItems {
		if item.kind == "worktree" {
			m.claudeMenuCursor = i
		}
	}
	if got := menuPreview(m); !strings.Contains(got, worktreeName(m.taskByID("p-9"))) || !strings.HasSuffix(got, "<task prompt>") {
		t.Errorf("the wrapped preview lost part of the command: %q", got)
	}
}
