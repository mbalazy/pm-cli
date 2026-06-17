package board

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

// execSession is one watchable worker transcript within a run (a sub for an
// epic, or the task itself for `pm work`).
type execSession struct {
	subID   string
	status  string
	session string
}

var (
	execAssistantStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#d0d0d0"})
	execToolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
	execResultStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9a9a9a", Dark: "#6a6a6a"})
)

// openExecutorView opens the live agent-view for task t (or, if t is a sub
// without its own run, its parent tracker's run). Returns false when there is
// no executor run to watch.
func (m *Model) openExecutorView(t *storage.Task) bool {
	if t == nil {
		return false
	}
	st := m.runStates[t.Meta.ID]
	if st == nil && t.Meta.Parent != "" {
		st = m.runStates[t.Meta.Parent]
	}
	if st == nil {
		return false
	}
	m.executorPrevView = m.currentView // dedicated: don't clobber detail's previousView
	m.currentView = viewExecutor
	m.executorRunTaskID = st.TaskID
	m.executorRunProj = st.Project
	m.executorFollow = true
	m.executorSessionIdx = 0
	m.executorViewport = viewport.New(m.width, executorBodyHeight(m.height))
	m.refreshExecutorView()
	// Default to the currently-running session if one is active.
	for i, s := range m.executorSessions {
		if st.CurrentSession != "" && s.session == st.CurrentSession {
			m.executorSessionIdx = i
		}
	}
	m.renderExecutorContent()
	m.executorViewport.GotoBottom()
	return true
}

// switchExecutorWorker cycles the displayed worker transcript by dir (+1/-1),
// with a toast so the switch is visible even when transcripts look similar.
func (m *Model) switchExecutorWorker(dir int) {
	n := len(m.executorSessions)
	if n <= 1 {
		m.toastMsg = "only one worker in this run"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return
	}
	m.executorSessionIdx = (m.executorSessionIdx + dir + n) % n
	m.executorFollow = true
	m.renderExecutorContent()
	m.executorViewport.GotoBottom()
	cur := m.executorSessions[m.executorSessionIdx]
	m.toastMsg = fmt.Sprintf("worker %d/%d: %s", m.executorSessionIdx+1, n, cur.subID)
	m.toastExpiry = time.Now().Add(2 * time.Second)
}

func executorBodyHeight(h int) int {
	// header (3) + footer (1)
	body := h - 4
	if body < 3 {
		body = 3
	}
	return body
}

// refreshExecutorView re-reads the run-state, rebuilds the watchable session
// list, and re-renders the transcript. Cheap; called on each tick while the
// agent-view is open.
func (m *Model) refreshExecutorView() {
	projDir := m.store.ProjectDir(m.executorRunProj)
	run, err := storage.ReadRunState(projDir, m.executorRunTaskID)
	if err != nil {
		m.executorRun = nil
		m.executorSessions = nil
		m.executorViewport.SetContent(helpStyle.Render("\n  Run-state is gone (deleted?). Press esc to go back."))
		return
	}
	m.executorRun = run

	var sess []execSession
	for _, s := range run.Subs {
		if s.Session != "" {
			sess = append(sess, execSession{subID: s.ID, status: s.Status, session: s.Session})
		}
	}
	m.executorSessions = sess
	if m.executorSessionIdx >= len(sess) {
		m.executorSessionIdx = max(0, len(sess)-1)
	}
	m.renderExecutorContent()
}

// renderExecutorContent decodes the selected session's transcript into the
// viewport, preserving scroll position unless following.
func (m *Model) renderExecutorContent() {
	if len(m.executorSessions) == 0 {
		m.executorViewport.SetContent(helpStyle.Render("\n  Waiting for the worker to start...\n  (no worker session recorded in the run yet)"))
		return
	}
	cur := m.executorSessions[m.executorSessionIdx]
	// Transcripts are keyed by the worker cwd (the git repo), not the pm data dir.
	repoDir := ""
	if m.executorRun != nil {
		repoDir = m.executorRun.RepoPath
	}
	path := resolveSessionPath(repoDir, cur.session)
	if path == "" {
		m.executorViewport.SetContent(helpStyle.Render("\n  Waiting for the worker transcript...\n  session " + cur.session))
		return
	}
	content := decodeTranscript(path, m.executorViewport.Width-2, m.executorVerbose)
	if strings.TrimSpace(content) == "" {
		content = helpStyle.Render("  (transcript empty so far)")
	}
	off := m.executorViewport.YOffset
	m.executorViewport.SetContent(content)
	if m.executorFollow {
		m.executorViewport.GotoBottom()
	} else {
		m.executorViewport.SetYOffset(off)
	}
}

func (m Model) updateExecutorView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open):
		m.currentView = m.executorPrevView
		m.reload()
		return m, nil

	case msg.String() == "v":
		m.executorVerbose = !m.executorVerbose
		m.renderExecutorContent()
		return m, nil

	case msg.String() == "ctrl+j":
		m.executorViewport.LineDown(10)
		m.executorFollow = m.executorViewport.AtBottom()
		return m, nil

	case msg.String() == "ctrl+k":
		m.executorViewport.LineUp(10)
		m.executorFollow = m.executorViewport.AtBottom()
		return m, nil

	case key.Matches(msg, common.Keys.Tab):
		m.switchExecutorWorker(1)
		return m, nil

	case key.Matches(msg, common.Keys.ShiftTab):
		m.switchExecutorWorker(-1)
		return m, nil

	case msg.String() == "f":
		m.executorFollow = !m.executorFollow
		if m.executorFollow {
			m.executorViewport.GotoBottom()
		}
		return m, nil

	case msg.String() == "r":
		m.refreshExecutorView()
		return m, nil

	default:
		var cmd tea.Cmd
		m.executorViewport, cmd = m.executorViewport.Update(msg)
		// Scrolling up off the bottom turns off auto-follow; returning to the
		// bottom turns it back on.
		m.executorFollow = m.executorViewport.AtBottom()
		return m, cmd
	}
}

func (m Model) viewExecutor() string {
	run := m.executorRun
	statusStr := "?"
	kind := ""
	if run != nil {
		statusStr = run.Status
		kind = run.Kind
	}

	// Status chip.
	var chip string
	switch {
	case run != nil && run.IsLive():
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9ECE6A")).Render("▶ running")
	case statusStr == storage.RunStatusFailed:
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F7768E")).Render("✗ failed")
	case statusStr == storage.RunStatusDone:
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7AA2F7")).Render("✓ done")
	default:
		chip = lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render("▷ " + statusStr)
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Executor · " + m.executorRunTaskID)
	header := title + "  " + chip
	if kind != "" {
		header += "  " + helpStyle.Render("("+kind+")")
	}
	if run != nil && run.Started != "" {
		header += "  " + helpStyle.Render("⏱ "+execElapsed(run.Started, run))
	}

	// Worker tabs (per sub with a session).
	var tabs string
	if len(m.executorSessions) > 0 {
		var parts []string
		for i, s := range m.executorSessions {
			label := fmt.Sprintf("%s %s", s.subID, execSubGlyph(s.status))
			if i == m.executorSessionIdx {
				parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(special).Render("▎"+label))
			} else {
				parts = append(parts, helpStyle.Render(" "+label))
			}
		}
		tabs = strings.Join(parts, "  ")
	} else {
		tabs = helpStyle.Render("no worker session yet")
	}

	follow := "off"
	if m.executorFollow {
		follow = "on"
	}
	mode := "compact"
	if m.executorVerbose {
		mode = "full"
	}
	worker := ""
	if n := len(m.executorSessions); n > 1 {
		worker = fmt.Sprintf("worker %d/%d · tab switch · ", m.executorSessionIdx+1, n)
	}
	footer := helpStyle.Render(worker + "v " + mode + " · f follow:" + follow + " · j/k C-j/k scroll · r · esc")

	return m.applyToast(strings.Join([]string{header, tabs, m.executorViewport.View(), footer}, "\n"))
}

// renderExecutorDashboard renders a run's live/last state for the task-detail
// view: a status chip + per-sub progress (running sub marked), driven by the
// run-state file (which persists after the run, so this doubles as the summary).
func renderExecutorDashboard(run *storage.RunState, width int) string {
	if run == nil {
		return ""
	}
	var chip string
	switch {
	case run.IsLive():
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9ECE6A")).Render("▶ running")
	case run.Status == storage.RunStatusFailed:
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F7768E")).Render("✗ failed")
	case run.Status == storage.RunStatusDone:
		chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7AA2F7")).Render("✓ done")
	default:
		chip = lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render("▷ " + run.Status)
	}
	hdr := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Executor run") + "  " + chip
	if run.Started != "" {
		hdr += helpStyle.Render("  ⏱ " + execElapsed(run.Started, run))
	}

	var b strings.Builder
	b.WriteString(hdr + "\n")
	for _, s := range run.Subs {
		glyph := execSubGlyph(s.Status)
		row := fmt.Sprintf("  %s  %-22s %-9s", glyph, s.ID, s.Status)
		if s.ID == run.CurrentSub && run.IsLive() {
			row += " ←"
		}
		if s.Note != "" {
			row += "  " + truncate(s.Note, max(20, width-44))
		}
		if s.Status == storage.RunStatusRunning {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A")).Render(row))
		} else {
			b.WriteString(execAssistantStyle.Render(row))
		}
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("  press W to watch the live transcript"))
	return b.String()
}

func execSubGlyph(status string) string {
	switch status {
	case storage.RunStatusRunning:
		return "▶"
	case "merged", "done":
		return "✓"
	case "blocked", "failed", "conflict":
		return "✗"
	default:
		return "·"
	}
}

// execElapsed renders elapsed time since started; for a finished run it uses
// Updated as the end stamp.
func execElapsed(started string, run *storage.RunState) string {
	t0, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return ""
	}
	end := time.Now()
	if run != nil && run.Status != storage.RunStatusRunning && run.Updated != "" {
		if t1, err := time.Parse(time.RFC3339, run.Updated); err == nil {
			end = t1
		}
	}
	d := end.Sub(t0)
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	mn := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, mn)
	}
	if mn > 0 {
		return fmt.Sprintf("%dm%ds", mn, s)
	}
	return fmt.Sprintf("%ds", s)
}

// --- transcript decoding ---

type tEvent struct {
	Type    string `json:"type"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type tBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   map[string]any  `json:"input"`
	Content json.RawMessage `json:"content"`
}

// decodeTranscript renders a worker session .jsonl into a readable agent feed.
// Compact (verbose=false): assistant text, tool calls (name + key arg), and the
// first line of each tool result. Full (verbose=true): the whole conversation -
// untruncated tool args/commands and complete, wrapped tool results.
func decodeTranscript(path string, width int, verbose bool) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev tEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, blk := range parseBlocks(ev.Message.Content) {
				switch blk.Type {
				case "text":
					if txt := strings.TrimSpace(blk.Text); txt != "" {
						b.WriteString(execAssistantStyle.Width(width).Render("● " + txt))
						b.WriteString("\n")
					}
				case "tool_use":
					arg := toolSummary(blk.Name, blk.Input)
					if verbose && blk.Name == "Bash" {
						if c, ok := blk.Input["command"].(string); ok {
							arg = strings.TrimSpace(c)
						}
					}
					row := "  🔧 " + blk.Name
					if arg != "" {
						row += "  " + arg
					}
					if verbose {
						b.WriteString(execToolStyle.Width(width).Render(row))
					} else {
						b.WriteString(execToolStyle.Render(truncate(row, width)))
					}
					b.WriteString("\n")
				}
			}
		case "user":
			for _, blk := range parseBlocks(ev.Message.Content) {
				if blk.Type == "tool_result" {
					res := blockResultText(blk)
					if verbose {
						if r := strings.TrimSpace(res); r != "" {
							b.WriteString(execResultStyle.Width(width).Render("     ⮑ " + r))
							b.WriteString("\n")
						}
					} else if r := firstLine(res); r != "" {
						b.WriteString(execResultStyle.Render(truncate("     ⮑ "+r, width)))
						b.WriteString("\n")
					}
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func parseBlocks(raw json.RawMessage) []tBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []tBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return []tBlock{{Type: "text", Text: s}}
	}
	return nil
}

func blockResultText(b tBlock) string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var arr []tBlock
	if json.Unmarshal(b.Content, &arr) == nil {
		var parts []string
		for _, x := range arr {
			if x.Text != "" {
				parts = append(parts, x.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func toolSummary(name string, input map[string]any) string {
	get := func(k string) string {
		if v, ok := input[k].(string); ok {
			return v
		}
		return ""
	}
	switch name {
	case "Write", "Edit", "Read", "NotebookEdit":
		return shortPath(get("file_path"))
	case "Bash":
		return firstLine(get("command"))
	case "Grep", "Glob":
		return get("pattern")
	case "Task":
		return get("description")
	case "TodoWrite":
		return ""
	default:
		if len(input) == 0 {
			return ""
		}
		raw, _ := json.Marshal(input)
		return truncate(string(raw), 60)
	}
}

func shortPath(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func truncate(s string, max int) string {
	if max < 4 {
		max = 4
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
