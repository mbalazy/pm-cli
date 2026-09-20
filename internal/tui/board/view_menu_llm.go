package board

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// The Claude/Codex launch overlay, rebuilt on the same three ideas as the
// executor one (view_menu_executor.go): group the launches by what they do,
// preview the command, and let a narrow terminal drop the teaching half rather
// than the border.
//
// What was wrong here was subtler than density. Every item named its VARIANT
// ("Here (takes over terminal)", "Worktree + tmux", "Resume in tmux") so the
// two questions the list actually answers - WHERE does this run, and WHICH
// session does it join - were spread across seven labels that each had to be
// read to be told apart. They are now two: the group says where (and names the
// directory, the branch, the session id), the item says here-or-tmux.

// llmGroupOf buckets a launch kind. Same rule as executorSectionOf: derived at
// render time, so the ORDER of claudeMenuItems stays the one thing the cursor
// and the shortcut keys index.
func llmGroupOf(kind string) string {
	switch kind {
	case "here", "tmux":
		return "repo"
	case "worktree", "worktree-tmux":
		return "worktree"
	case "resume", "resume-tmux":
		return "resume"
	case "fork", "fork-tmux":
		return "fork"
	}
	return ""
}

// llmItemHint is the dim half-sentence after a launch's name.
func llmItemHint(kind string) string {
	switch kind {
	case "here", "worktree", "resume", "fork":
		return "takes over this terminal"
	case "tmux", "worktree-tmux", "resume-tmux", "fork-tmux":
		return "new tmux window"
	case "project":
		return "the whole project as context, no task"
	}
	return ""
}

// llmPreview is the command the highlighted launch would run, with the parts
// that do not exist yet named rather than invented: a session id is minted at
// launch, and the prompt is a page of task context nobody wants echoed here.
//
// Deliberately built from the same arg helpers the launches use
// (claudeResumeArgs and friends), so a flag that changes there changes here.
func (m Model) llmPreview(kind string, t *storage.Task) string {
	if kind == "project" {
		return "(opens the project-scope menu)"
	}
	skip := ""
	if m.claudeMenuSkipPerms {
		skip = "--dangerously-skip-permissions"
	}
	if m.launchAgent == launchAgentCodex {
		bypass := ""
		if m.claudeMenuSkipPerms {
			bypass = "--dangerously-bypass-approvals-and-sandbox "
		}
		return "codex " + bypass + "<task prompt>"
	}

	const newSession = "<new session>"
	switch kind {
	case "worktree", "worktree-tmux":
		wt := "<branch>"
		if t != nil {
			wt = worktreeName(t)
		}
		return "claude " + strings.Join(claudeWorktreeArgs(wt, newSession, skip, "<task prompt>"), " ")
	case "resume", "resume-tmux":
		return "claude " + strings.Join(claudeResumeArgs(m.llmResumeID(t), skip), " ")
	case "fork", "fork-tmux":
		return "claude " + strings.Join(claudeForkArgs(m.llmResumeID(t), newSession, skip), " ")
	}
	return "claude " + strings.Join(claudeSessionArgs(newSession, skip, "<task prompt>"), " ")
}

// llmResumeID is the session a resume/fork would attach to: the one the session
// menu picked, else the task's most recent - the same precedence launchClaude
// applies.
func (m Model) llmResumeID(t *storage.Task) string {
	if m.resumeSessionID != "" {
		return m.resumeSessionID
	}
	if t != nil {
		if s := lastSession(t); s != "" {
			return s
		}
	}
	return "<session>"
}

func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// llmMenuTitle names the agent in the title instead of spending a line on an
// "Agent:" row - it is the choice every item below depends on, so it belongs
// where the eye lands first.
func (m Model) llmMenuTitle() string {
	if m.resumeOnly && m.launchAgent != launchAgentCodex {
		if m.forkMode {
			return "Fork session " + shortSession(m.resumeSessionID)
		}
		return "Resume session " + shortSession(m.resumeSessionID)
	}
	return "Launch " + m.launchAgent.label()
}

func (m Model) viewLLMMenu() string {
	if body, ok := m.llmMenuBody(false); ok {
		return m.placeMenuBox(body)
	}
	body, _ := m.llmMenuBody(true)
	return m.placeMenuBox(body)
}

// llmMenuBody builds the overlay's lines and reports whether they fit the
// terminal's width. See executorMenuBody: compact drops the hints and the
// group subtitles, never a key or the preview.
func (m Model) llmMenuBody(compact bool) ([]string, bool) {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(highlight)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
	onStyle := lipgloss.NewStyle().Bold(true).Foreground(special)

	t := m.menuTask()
	subject := ""
	switch {
	case m.projectScopeLaunch:
		subject = m.projectScopeSlug
	case t != nil:
		subject = t.Meta.ID
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(m.llmMenuTitle())+
		"  "+dimStyle.Render(subject))
	lines = append(lines, "")

	// Group subtitles answer "where does this run" once, with the actual
	// directory / branch / session rather than the word for it.
	repoDir := ""
	if slug := m.launchProjectSlug(t); slug != "" && m.store != nil {
		if proj, err := m.store.GetProject(slug); err == nil {
			repoDir = shortenPath(proj.Path)
		}
	}
	wtName := ""
	if t != nil {
		wtName = worktreeName(t)
	}
	// Clamped for the same reason as the preview: a repo path is as long as
	// somebody's directory tree.
	budget := menuContentWidth(m.width)
	groupTitle := map[string]string{
		"repo":     truncateWidth(joinTitle("IN THE REPO", repoDir, compact), budget),
		"worktree": truncateWidth(joinTitle("ISOLATED WORKTREE", wtName+" · own branch, dev-server port and simulator", compact), budget),
		"resume":   truncateWidth(joinTitle("RESUME", shortSession(m.llmResumeID(t)), compact), budget),
		"fork":     truncateWidth(joinTitle("FORK FROM", shortSession(m.llmResumeID(t)), compact), budget),
	}

	nameW := 0
	for _, item := range m.claudeMenuItems {
		if w := lipgloss.Width(item.label); w > nameW {
			nameW = w
		}
	}

	lastGroup := "none"
	for i, item := range m.claudeMenuItems {
		g := llmGroupOf(item.kind)
		if g != lastGroup {
			// In compact the header alone separates the groups, and the blank
			// line above it is the first thing worth its rows of height. A
			// group with NO header still needs one, or its items read as the
			// tail of the group above.
			if lastGroup != "none" && (!compact || groupTitle[g] == "") {
				lines = append(lines, "")
			}
			if title := groupTitle[g]; title != "" {
				lines = append(lines, "  "+helpStyle.Render(title))
			}
			lastGroup = g
		}
		prefix := "    "
		labelStyle := dimStyle
		if i == m.claudeMenuCursor {
			prefix = "  > "
			labelStyle = onStyle
		}
		row := prefix + keyStyle.Render(item.shortcut) + "  " + labelStyle.Render(padRight(item.label, nameW))
		if hint := llmItemHint(item.kind); hint != "" && !compact {
			row += "   " + helpStyle.Render(hint)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")

	// One chip, and the agent switch beside it - the two things that change
	// what the preview below says without moving the cursor.
	box := "[ ]"
	chipStyle := dimStyle
	if m.claudeMenuSkipPerms {
		box = "[x]"
		// Red, alone among the chips: this is the one that removes a guard.
		chipStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	}
	permsLabel := "skip permissions"
	if m.launchAgent == launchAgentCodex {
		permsLabel = "bypass sandbox"
	}
	// The agent is shown even in the resume sub-menu: @ cycles there too, and
	// Codex having no resume at all is exactly the thing worth seeing first.
	chips := chipStyle.Render(box+" ! "+permsLabel) + "   " + helpStyle.Render("@ agent: "+m.launchAgent.label())
	lines = append(lines, "  "+chips)

	// The preview is spliced in after the width is known - see the same
	// comment in executorMenuBody, and view_menu_common.go for why.
	help := "  ↑↓ move · enter or its key runs it · ! perms · @ agent · esc back"
	if compact {
		help = "  ↑↓ move · enter runs · ! perms · @ agent · esc back"
	}
	tail := []string{"", helpStyle.Render(help)}

	if m.claudeMenuCursor < len(m.claudeMenuItems) {
		width := menuBodyWidth(append(append([]string{}, lines...), tail...), m.width)
		all := make([]string, 0, len(m.claudeMenuItems))
		for _, item := range m.claudeMenuItems {
			all = append(all, m.llmPreview(item.kind, t))
		}
		cur := m.llmPreview(m.claudeMenuItems[m.claudeMenuCursor].kind, t)
		lines = append(lines, "")
		lines = append(lines, renderPreview(cur, width, tallestPreview(all, width), dimStyle)...)
	}
	lines = append(lines, tail...)

	return lines, m.menuBoxFits(lines)
}

// launchProjectSlug is which project this overlay is launching into - the
// project-scope menu carries its own slug, every other launch takes the task's.
func (m Model) launchProjectSlug(t *storage.Task) string {
	if m.projectScopeLaunch {
		return m.projectScopeSlug
	}
	if t != nil {
		return t.Project
	}
	return ""
}
