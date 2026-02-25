package board

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
	"github.com/mbalazy/pm/internal/version"
)

type view int

const (
	viewBoard view = iota
	viewDetail
)

type Model struct {
	store         *storage.Store
	tasks         []*storage.Task
	projects      []string
	statuses      []storage.TaskStatus
	activeProject int // 0 = all, 1+ = specific project
	activeCol     int
	cursors       []int
	width         int
	height        int
	currentView   view
	detailContent string
	err           error
}

func New(store *storage.Store, filterProject string) Model {
	m := Model{
		store:  store,
		width:  80,
		height: 24,
	}

	projects, _ := store.ListProjects()
	m.projects = append([]string{"all"}, projects...)

	if filterProject != "" {
		for i, p := range m.projects {
			if p == filterProject {
				m.activeProject = i
				break
			}
		}
	}

	m.loadStatuses()
	m.cursors = make([]int, len(m.statuses))
	m.loadTasks()
	return m
}

func (m *Model) loadStatuses() {
	if m.activeProject == 0 {
		m.statuses = m.store.GetAllStatuses()
	} else {
		slug := m.projects[m.activeProject]
		m.statuses = m.store.GetProjectStatuses(slug)
	}
}

func (m *Model) loadTasks() {
	if m.activeProject == 0 {
		m.tasks, m.err = m.store.GetAllTasks()
	} else {
		slug := m.projects[m.activeProject]
		m.tasks, m.err = m.store.GetTasks(slug)
	}
}

func (m Model) filteredTasks(status storage.TaskStatus) []*storage.Task {
	var result []*storage.Task
	for _, t := range m.tasks {
		if t.Meta.Status == status {
			result = append(result, t)
		}
	}
	return result
}

func (m Model) columnTasks(col int) []*storage.Task {
	if col < 0 || col >= len(m.statuses) {
		return nil
	}
	return m.filteredTasks(m.statuses[col])
}

func (m Model) selectedTask() *storage.Task {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) == 0 {
		return nil
	}
	idx := m.cursors[m.activeCol]
	if idx >= len(tasks) {
		idx = len(tasks) - 1
	}
	return tasks[idx]
}

type tickMsg time.Time

const refreshInterval = 2 * time.Second

func doTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	return doTick()
}

type reloadMsg struct{}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		prevCount := len(m.tasks)
		m.loadTasks()
		if len(m.tasks) != prevCount {
			m.fixCursors()
		}
		return m, doTick()

	case reloadMsg:
		m.loadTasks()
		return m, doTick()

	case tea.KeyMsg:
		if m.currentView == viewDetail {
			return m.updateDetail(msg)
		}
		return m.updateBoard(msg)
	}
	return m, nil
}

func (m Model) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, common.Keys.Left):
		if m.activeCol > 0 {
			m.activeCol--
		}

	case key.Matches(msg, common.Keys.Right):
		if m.activeCol < len(m.statuses)-1 {
			m.activeCol++
		}

	case key.Matches(msg, common.Keys.Up):
		if m.cursors[m.activeCol] > 0 {
			m.cursors[m.activeCol]--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if m.cursors[m.activeCol] < len(tasks)-1 {
			m.cursors[m.activeCol]++
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.loadStatuses()
		m.loadTasks()
		m.cursors = make([]int, len(m.statuses))
		if m.activeCol >= len(m.statuses) {
			m.activeCol = 0
		}

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.loadStatuses()
		m.loadTasks()
		m.cursors = make([]int, len(m.statuses))
		if m.activeCol >= len(m.statuses) {
			m.activeCol = 0
		}

	case key.Matches(msg, common.Keys.MoveBack):
		t := m.selectedTask()
		if t != nil {
			idx := m.statusIndex(t.Meta.Status)
			if idx > 0 {
				t.Meta.Status = m.statuses[idx-1]
			} else {
				t.Meta.Status = m.statuses[len(m.statuses)-1]
			}
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.loadTasks()
			m.fixCursors()
		}

	case key.Matches(msg, common.Keys.Move):
		t := m.selectedTask()
		if t != nil {
			idx := m.statusIndex(t.Meta.Status)
			t.Meta.Status = m.statuses[(idx+1)%len(m.statuses)]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.loadTasks()
			m.fixCursors()
		}

	case key.Matches(msg, common.Keys.Enter):
		t := m.selectedTask()
		if t != nil {
			m.currentView = viewDetail
			m.detailContent = renderTaskDetail(t)
		}

	case key.Matches(msg, common.Keys.Edit):
		t := m.selectedTask()
		if t != nil {
			return m, openEditor(t.FilePath)
		}
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit):
		m.currentView = viewBoard
		m.loadTasks() // reload in case file was edited
	}
	return m, nil
}

func (m Model) statusIndex(s storage.TaskStatus) int {
	for i, st := range m.statuses {
		if st == s {
			return i
		}
	}
	return 0
}

func (m *Model) fixCursors() {
	for i := range m.statuses {
		tasks := m.columnTasks(i)
		if m.cursors[i] >= len(tasks) && len(tasks) > 0 {
			m.cursors[i] = len(tasks) - 1
		}
	}
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return reloadMsg{}
	})
}

func renderTaskDetail(t *storage.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s", t.Meta.Title)
	if t.Meta.ID != "" {
		fmt.Fprintf(&sb, " (#%s)", t.Meta.ID)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", t.Meta.Status, t.Project, t.Meta.Updated)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "\n**Branch:** `%s`\n", t.Meta.Branch)
	}
	if t.Body != "" {
		fmt.Fprintf(&sb, "\n---\n\n%s\n", t.Body)
	}

	rendered, err := glamour.Render(sb.String(), "dark")
	if err != nil {
		return sb.String()
	}
	return rendered
}

func (m Model) View() string {
	if m.currentView == viewDetail {
		return m.viewDetail()
	}
	return m.viewBoard()
}

func (m Model) viewDetail() string {
	var sb strings.Builder
	sb.WriteString(m.detailContent)
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render("esc/q: back to board"))
	return sb.String()
}

func (m Model) viewBoard() string {
	var sb strings.Builder

	// title
	sb.WriteString(titleStyle.Render("pm board"))
	sb.WriteString("\n")

	// project tabs
	var tabs []string
	for i, p := range m.projects {
		name := p
		if i == 0 {
			name = "ALL"
		}
		if i == m.activeProject {
			tabs = append(tabs, activeTabStyle.Render("["+name+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(name))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	// columns
	numCols := len(m.statuses)
	if numCols == 0 {
		numCols = 1
	}
	colWidth := (m.width - 8) / numCols
	if colWidth < 20 {
		colWidth = 20
	}
	maxCardHeight := m.height - 10
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}

	var cols []string
	for i, status := range m.statuses {
		tasks := m.filteredTasks(status)
		isActive := i == m.activeCol

		header := strings.ToUpper(string(status))
		var content strings.Builder
		content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("%s (%d)", header, len(tasks))))
		content.WriteString("\n")

		for j, t := range tasks {
			isSelected := isActive && j == m.cursors[i]
			card := renderCard(t, colWidth-6, isSelected)
			content.WriteString(card)
			content.WriteString("\n")
		}

		style := columnStyle
		if isActive {
			style = activeColumnStyle
		}
		col := style.Width(colWidth).Height(maxCardHeight).Render(content.String())
		cols = append(cols, col)
	}

	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	sb.WriteString("\n")

	// status bar
	ver := helpStyle.Render("pm " + version.Version)
	help := helpStyle.Render("←/→ column  ↑/↓ navigate  enter detail  m/M move  e edit  tab/S-tab project  q quit")
	gap := m.width - lipgloss.Width(ver) - lipgloss.Width(help)
	if gap < 1 {
		gap = 1
	}
	sb.WriteString(ver + strings.Repeat(" ", gap) + help)

	return sb.String()
}

func renderCard(t *storage.Task, width int, selected bool) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	title := t.Meta.Title
	if t.Meta.ID != "" {
		title = "#" + t.Meta.ID + " " + title
	}
	// truncate title if too long
	if len(title) > width-2 {
		title = title[:width-5] + "..."
	}
	lines = append(lines, cardTitleStyle.Render(title))
	lines = append(lines, cardProjectStyle.Render(t.Project))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	return style.Render(strings.Join(lines, "\n"))
}
