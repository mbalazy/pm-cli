package board

import (
	"encoding/json"
	"fmt"
	"io"
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
//
// The RUN opens by default and the acceptance only when there is no run at all
// - never the other way round, even with both live: the run is where a worker
// is writing code, and the acceptance is one W away.
func (m *Model) openExecutorView(t *storage.Task) bool {
	if t == nil {
		return false
	}
	st := m.runForTask(t)
	kind := ""
	if st != nil {
		kind = st.Kind // "work" / "run-epic" - anything but the acceptance
	} else if st = m.finishForTask(t); st != nil {
		kind = storage.RunKindFinish
	}
	if st == nil {
		return false
	}
	m.executorPrevView = m.currentView // dedicated: don't clobber detail's previousView
	m.currentView = viewExecutor
	m.executorRunTaskID = st.TaskID
	m.executorRunProj = st.Project
	m.executorRunKind = kind
	m.executorFollow = true
	m.executorSessionIdx = 0
	m.executorViewport = viewport.New(m.width, executorBodyHeight(m.height))
	m.refreshExecutorView()
	m.executorSessionIdx = defaultSessionIdx(m.executorSessions, st.CurrentSession)
	m.renderExecutorContent()
	m.executorViewport.GotoBottom()
	return true
}

// defaultSessionIdx picks which worker transcript the agent-view opens on: the
// one in flight when there is one, else the MOST RECENT worker. current is
// empty whenever no worker is running - between subs, and through the
// post-worker tail (result recording, merge, push) - and there the sub that
// just ran is the interesting transcript, not the run's first sub.
func defaultSessionIdx(sessions []execSession, current string) int {
	if len(sessions) == 0 {
		return 0
	}
	for i, s := range sessions {
		if current != "" && s.session == current {
			return i
		}
	}
	return len(sessions) - 1
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

// watchingFinish reports whether the agent-view is showing the ACCEPTANCE of
// the run rather than the run itself.
func (m Model) watchingFinish() bool { return m.executorRunKind == storage.RunKindFinish }

// otherRunKind is the kind W switches to.
func (m Model) otherRunKind() string {
	if m.watchingFinish() {
		return storage.RunKindEpic // any non-finish kind routes to the run's own file
	}
	return storage.RunKindFinish
}

// otherRunKindLabel names the counterpart for a toast/hint, in the words the
// rest of pm uses for the two: a run, and the acceptance (odbiór) of one.
func (m Model) otherRunKindLabel() string {
	if m.watchingFinish() {
		return "the executor run"
	}
	return "the acceptance run"
}

// otherRunKindExists reports whether the counterpart run-state is on disk. Read
// straight from the file rather than from the board's maps: those hold only the
// projects currently on screen, and the agent-view can be open on a run whose
// project the user has since tabbed away from.
func (m Model) otherRunKindExists() bool {
	if m.store == nil || m.executorRunTaskID == "" {
		return false
	}
	_, err := readRunStateOfKind(m.store.ProjectDir(m.executorRunProj), m.executorRunTaskID, m.otherRunKind())
	return err == nil
}

// switchExecutorRunKind flips the agent-view between a run and its acceptance.
// Deliberately a toggle INSIDE this view rather than a second key on the board:
// the two runs are one task's story, and the board already spends X/W/K on the
// executor.
func (m *Model) switchExecutorRunKind() {
	if !m.otherRunKindExists() {
		what := "acceptance run"
		if m.watchingFinish() {
			what = "executor run"
		}
		m.toastMsg = "no " + what + " recorded for " + m.executorRunTaskID
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return
	}
	m.executorRunKind = m.otherRunKind()
	// Re-derive everything the previous kind's state seeded: which transcripts
	// are watchable, which one is in flight, and the scroll position.
	m.executorSessionIdx = 0
	m.executorFollow = true
	m.refreshExecutorView()
	if m.executorRun != nil {
		m.executorSessionIdx = defaultSessionIdx(m.executorSessions, m.executorRun.CurrentSession)
	}
	m.renderExecutorContent()
	m.executorViewport.GotoBottom()
	what := "executor run"
	if m.watchingFinish() {
		what = "acceptance run"
	}
	m.toastMsg = "watching the " + what + " for " + m.executorRunTaskID
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
	// The counterpart is re-checked on every refresh, not once when the view
	// opened: an acceptance routinely STARTS while its run is being watched
	// (batch-finish-auto accepts subs as they land), and a toggle hint that only
	// reflected the moment of opening would hide it until the view was reopened.
	m.executorHasOther = m.otherRunKindExists()
	run, err := readRunStateOfKind(projDir, m.executorRunTaskID, m.executorRunKind)
	if err != nil {
		m.executorRun = nil
		m.executorSessions = nil
		msg := "\n  Run-state is gone (deleted?). Press esc to go back."
		if m.executorHasOther {
			msg += "\n  Press W to switch to " + m.otherRunKindLabel() + "."
		}
		m.executorViewport.SetContent(helpStyle.Render(msg))
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

// decodedTranscript decodes path via this Model's per-path incremental cache,
// so repeated ticks on the same transcript only pay for newly appended bytes.
func (m *Model) decodedTranscript(path string, width int, verbose bool) string {
	if m.transcriptCaches == nil {
		m.transcriptCaches = make(map[string]*transcriptCache)
	}
	c := m.transcriptCaches[path]
	if c == nil {
		c = &transcriptCache{}
		m.transcriptCaches[path] = c
	}
	return c.render(path, width, verbose)
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
	configDir := ""
	if m.executorRun != nil {
		repoDir = m.executorRun.RepoPath
		configDir = m.claudeConfigDir(m.executorRun.Project)
	}
	path := resolveSessionPath(repoDir, cur.session, configDir)
	if path == "" {
		m.executorViewport.SetContent(helpStyle.Render("\n  Waiting for the worker transcript...\n  session " + cur.session))
		return
	}
	content := m.decodedTranscript(path, m.executorViewport.Width-2, m.executorVerbose)
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
	// Kill confirmation: any key other than K cancels the pending kill.
	if m.confirmAction == "kill-run" && !key.Matches(msg, common.Keys.KillRun) {
		m.confirmAction = ""
		m.confirmTaskID = ""
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open):
		m.currentView = m.executorPrevView
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.KillRun):
		st := m.executorRun
		if st == nil || !st.IsLive() {
			m.toastMsg = "no live run to stop"
			m.toastExpiry = time.Now().Add(2 * time.Second)
			return m, nil
		}
		if m.confirmAction == "kill-run" && m.confirmTaskID == st.TaskID {
			m.confirmAction = ""
			m.confirmTaskID = ""
			cmd := m.killRun(st)
			m.refreshExecutorView()
			return m, cmd
		}
		m.confirmAction = "kill-run"
		m.confirmTaskID = st.TaskID
		return m, nil

	case key.Matches(msg, common.Keys.WatchExecutor):
		// The key that opened this view switches which of the task's two runs it
		// shows. No new board key: W already means "watch this task's run", and
		// here it means "watch the other one".
		m.switchExecutorRunKind()
		return m, nil

	case msg.String() == "v":
		m.executorVerbose = !m.executorVerbose
		m.renderExecutorContent()
		return m, nil

	case msg.String() == "ctrl+j":
		m.executorViewport.ScrollDown(10)
		m.executorFollow = m.executorViewport.AtBottom()
		return m, nil

	case msg.String() == "ctrl+k":
		m.executorViewport.ScrollUp(10)
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

	label := "Executor"
	if m.watchingFinish() {
		// Named for what it is, not for the command: this is the odbiór, and a
		// header reading "Executor" over it would make the two runs of one
		// tracker indistinguishable at a glance - which is the whole reason they
		// are separate states.
		label = "Acceptance"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(label + " · " + m.executorRunTaskID)
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
	killHint := ""
	if run != nil && run.IsLive() {
		killHint = " · K kill"
	}
	switchHint := ""
	if m.executorHasOther {
		switchHint = " · W " + m.otherRunKindLabel()
	}
	var footer string
	if m.confirmAction == "kill-run" {
		stopped := "parks the worker on waiting"
		if m.watchingFinish() {
			// The acceptance parks nothing (see killRun) - promising it here would
			// be the confirmation prompt describing a different action.
			stopped = "releases the acceptance claim"
		}
		footer = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F7768E")).Render("press K again to STOP this run (" + stopped + ") · any other key cancels")
	} else {
		footer = helpStyle.Render(worker + "v " + mode + " · f follow:" + follow + " · j/k C-j/k scroll · r" + killHint + switchHint + " · esc")
	}

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
	title := "Executor run"
	if run.Kind == storage.RunKindFinish {
		title = "Acceptance run (pm finish)"
	}
	hdr := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(title) + "  " + chip
	if run.Started != "" {
		hdr += helpStyle.Render("  ⏱ " + execElapsed(run.Started, run))
	}
	// The executor re-stamps Updated every ~30s, but ONLY while a worker is
	// actually in flight - so the age is only a signal in that same window,
	// which CurrentSession marks (pinned right before the worker spawns, cleared
	// the moment it returns, alongside the heartbeat's own stop). Rendering it on
	// any live run would read as "hung" through every legitimate non-worker
	// stretch: the board seeds a running run-state the moment it launches the
	// process, and the executor does its worktree seeding, `executor.prepare` and
	// baseline capture (15-min caps each) before it ever owns the file.
	if run.IsLive() && run.CurrentSession != "" {
		if age := execHeartbeatAge(run.Updated); age != "" {
			hdr += helpStyle.Render("  ♥ " + age)
		}
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
	hint := "  press W to watch the live transcript"
	if run.Kind == storage.RunKindFinish {
		hint += " (W again switches run ↔ acceptance)"
	}
	b.WriteString(helpStyle.Render(hint))
	return b.String()
}

func execSubGlyph(status string) string {
	switch status {
	case storage.RunStatusRunning:
		return "▶"
	case "merged", "pushed", "verified", "done":
		return "✓"
	case "blocked", "failed", "conflict":
		return "✗"
	case "partial":
		// An acceptance verdict of its own: it accepted, with work left over.
		// Without a glyph it fell through to "·", which reads as "not started".
		return "◐"
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
	return shortDur(end.Sub(t0))
}

// execHeartbeatAge renders how long ago the run last stamped its run-state
// (storage.HeartbeatInterval while a worker is in flight). Empty when the stamp
// is missing or unparseable - an absent heartbeat must not render as "0s ago".
func execHeartbeatAge(updated string) string {
	t, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return ""
	}
	return shortDur(time.Since(t)) + " ago"
}

// shortDur renders a duration compactly (1h2m / 3m4s / 5s), clamping negatives
// to zero (clock skew between the writing executor and the reading board).
func shortDur(d time.Duration) string {
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
//
// This always reads and re-parses the whole file - it exists for one-shot
// callers (tests) and as the building block for transcriptCache, which is what
// the live agent-view actually uses to avoid doing this on every tick.
func decodeTranscript(path string, width int, verbose bool) string {
	c := &transcriptCache{}
	return c.render(path, width, verbose)
}

// appendTranscriptLine decodes one .jsonl line and, if it renders to visible
// output (assistant text/tool-use, or a user tool_result), writes it to b.
func appendTranscriptLine(b *strings.Builder, line string, width int, verbose bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var ev tEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
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

// transcriptCache incrementally decodes a growing worker transcript: a tick
// with an unchanged file (same size + mtime) skips the read entirely, and an
// appended file re-reads/re-parses only the bytes written since the last call
// - never the whole file. width/verbose changes, or the file shrinking
// (rotated/truncated), force a full reset and re-decode from byte 0.
type transcriptCache struct {
	width    int
	verbose  bool
	size     int64
	modTime  time.Time
	pending  []byte // unterminated trailing line bytes from the last read
	rendered strings.Builder
}

// render returns the decoded feed for path, reading/parsing only what changed
// since the previous call on this cache.
func (c *transcriptCache) render(path string, width int, verbose bool) string {
	if width < 20 {
		width = 20
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if c.width != width || c.verbose != verbose || info.Size() < c.size {
		*c = transcriptCache{width: width, verbose: verbose}
	}
	if info.Size() == c.size && info.ModTime().Equal(c.modTime) {
		return strings.TrimRight(c.rendered.String(), "\n")
	}

	f, err := os.Open(path)
	if err != nil {
		return strings.TrimRight(c.rendered.String(), "\n")
	}
	defer f.Close()
	readFrom := c.size
	if _, err := f.Seek(readFrom, io.SeekStart); err != nil {
		return strings.TrimRight(c.rendered.String(), "\n")
	}
	newData, err := io.ReadAll(f)
	if err != nil {
		return strings.TrimRight(c.rendered.String(), "\n")
	}

	buf := append(c.pending, newData...)
	parts := strings.Split(string(buf), "\n")
	complete, leftover := parts[:len(parts)-1], parts[len(parts)-1]
	for _, line := range complete {
		appendTranscriptLine(&c.rendered, line, width, verbose)
	}
	c.pending = []byte(leftover)
	// The file may have grown further between the Stat above and this read
	// finishing (a live worker still writing) - ReadAll reads to the ACTUAL
	// EOF at read time, which can exceed info.Size(). Deriving the new offset
	// from bytes actually consumed (not the stale pre-read Stat) is what makes
	// this safe to re-seek from on the next tick; stamping info.Size() here
	// would understate it and cause the next call to re-decode (duplicate)
	// the tail this call already rendered.
	c.size = readFrom + int64(len(newData))
	if post, err := f.Stat(); err == nil {
		c.modTime = post.ModTime()
	} else {
		c.modTime = info.ModTime()
	}
	return strings.TrimRight(c.rendered.String(), "\n")
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

// truncate is the executor view's historical name for display-width-safe
// truncation; the byte-slicing it used to do split UTF-8 sequences (Polish
// diacritics, emoji) into mojibake.
func truncate(s string, max int) string {
	if max < 4 {
		max = 4
	}
	return truncateWidth(s, max)
}
