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
	activeProject int // 0 = all, 1+ = specific project
	activeCol     int // 0=todo, 1=doing, 2=done
	cursors       [3]int
	width         int
	height        int
	currentView   view
	detailContent string
	err           error
}

func New(store *storage.Store, filterProject string) Model {
	m := Model{
		store: store,
		width: 80,
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

	m.loadTasks()
	return m
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
	statuses := []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusDone}
	return m.filteredTasks(statuses[col])
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

func (m Model) Init() tea.Cmd {
	return nil
}

type reloadMsg struct{}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case reloadMsg:
		m.loadTasks()
		return m, nil

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
		if m.activeCol < 2 {
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
		m.loadTasks()
		m.cursors = [3]int{}

	case key.Matches(msg, common.Keys.Move):
		t := m.selectedTask()
		if t != nil {
			nextStatus := map[storage.TaskStatus]storage.TaskStatus{
				storage.StatusTodo:  storage.StatusDoing,
				storage.StatusDoing: storage.StatusDone,
				storage.StatusDone:  storage.StatusTodo,
			}
			t.Meta.Status = nextStatus[t.Meta.Status]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.loadTasks()
			// fix cursor if needed
			for i := range m.cursors {
				tasks := m.columnTasks(i)
				if m.cursors[i] >= len(tasks) && len(tasks) > 0 {
					m.cursors[i] = len(tasks) - 1
				}
			}
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
	colWidth := (m.width - 8) / 3
	if colWidth < 20 {
		colWidth = 20
	}
	maxCardHeight := m.height - 10
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}

	statuses := []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusDone}
	headers := []string{"TODO", "DOING", "DONE"}

	var cols []string
	for i, status := range statuses {
		tasks := m.filteredTasks(status)
		isActive := i == m.activeCol

		var content strings.Builder
		content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("%s (%d)", headers[i], len(tasks))))
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

	// help bar
	help := "←/→ column  ↑/↓ navigate  enter detail  m move  e edit  tab project  q quit"
	sb.WriteString(helpStyle.Render(help))

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
