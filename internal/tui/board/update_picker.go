package board

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

func (m *Model) openProjectPicker() {
	m.pickerItems = nil
	m.pickerCursor = 0
	m.pickerFilter = ""
	m.pickerInput.SetValue("")
	m.pickerInput.Blur()

	allTasks, _ := m.store.GetAllTasks()

	// aggregate per-project stats
	type projectStats struct {
		statusCounts map[storage.TaskStatus]int
		lastUpdated  string
	}
	stats := make(map[string]*projectStats)
	for _, t := range allTasks {
		if t.Meta.Status == storage.StatusArchived {
			continue
		}
		s, ok := stats[t.Project]
		if !ok {
			s = &projectStats{statusCounts: make(map[storage.TaskStatus]int)}
			stats[t.Project] = s
		}
		s.statusCounts[t.Meta.Status]++
		if t.Meta.Updated > s.lastUpdated {
			s.lastUpdated = t.Meta.Updated
		}
	}

	for _, slug := range m.projects[1:] { // skip "all"
		proj, _ := m.store.GetProject(slug)
		name := slug
		stack := ""
		var path, repo string
		var links map[string]string
		var tags []string
		if proj != nil {
			if proj.Name != "" {
				name = proj.Name
			}
			stack = proj.Stack
			path = proj.Path
			repo = proj.Repo
			links = proj.Links
			tags = proj.Tags
		}
		item := pickerItem{
			slug:     slug,
			name:     name,
			stack:    stack,
			hidden:   m.hiddenProjects[slug],
			path:     path,
			repo:     repo,
			links:    links,
			tags:     tags,
			statuses: m.store.GetProjectStatuses(slug),
		}
		if s, ok := stats[slug]; ok {
			item.statusCounts = s.statusCounts
			item.lastUpdated = s.lastUpdated
			total := 0
			for _, c := range s.statusCounts {
				total += c
			}
			item.taskCount = total
			item.doingCount = s.statusCounts[storage.ParseStatus("doing")]
		}
		m.pickerItems = append(m.pickerItems, item)
	}

	m.sortPickerItems()
	m.projectPicker = true
}

func (m *Model) sortPickerItems() {
	sort.SliceStable(m.pickerItems, func(i, j int) bool {
		a, b := m.pickerItems[i], m.pickerItems[j]
		// only rule: hidden projects sink to the bottom
		if a.hidden != b.hidden {
			return !a.hidden
		}
		return false // preserve insertion order (from m.projects)
	})
}

func (m Model) filteredPickerItems() []pickerItem {
	if m.pickerFilter == "" {
		return m.pickerItems
	}
	q := strings.ToLower(m.pickerFilter)
	var result []pickerItem
	for _, item := range m.pickerItems {
		if strings.Contains(strings.ToLower(item.name), q) ||
			strings.Contains(strings.ToLower(item.slug), q) ||
			strings.Contains(strings.ToLower(item.stack), q) {
			result = append(result, item)
		}
	}
	return result
}

func (m *Model) saveTUIState() {
	var hidden []string
	for slug, h := range m.hiddenProjects {
		if h {
			hidden = append(hidden, slug)
		}
	}
	sort.Strings(hidden)
	saveTUIConfig(m.store.RootDir(), tuiConfig{
		HiddenProjects: hidden,
		ProjectOrder:   m.projects[1:], // skip "all"
	})
}

// updateProjectPicker owns the keyboard while the project picker is up. The
// yank menu can be opened ON TOP of it (Y) and outranks it in overlayLadder,
// so this function is never reached with that menu open.
func (m Model) updateProjectPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// handle filter input mode
	if m.pickerInput.Focused() {
		switch {
		case key.Matches(msg, common.Keys.Enter):
			m.pickerFilter = m.pickerInput.Value()
			m.pickerInput.Blur()
			m.pickerCursor = 0
			return m, nil
		case key.Matches(msg, common.Keys.Escape):
			if m.pickerFilter != "" || m.pickerInput.Value() != "" {
				m.pickerFilter = ""
				m.pickerInput.SetValue("")
				m.pickerInput.Blur()
				m.pickerCursor = 0
				return m, nil
			}
			m.pickerInput.Blur()
			m.projectPicker = false
			return m, nil
		default:
			var cmd tea.Cmd
			m.pickerInput, cmd = m.pickerInput.Update(msg)
			m.pickerFilter = m.pickerInput.Value()
			m.pickerCursor = 0
			return m, cmd
		}
	}

	items := m.filteredPickerItems()

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit):
		if m.pickerFilter != "" {
			m.pickerFilter = ""
			m.pickerInput.SetValue("")
			m.pickerCursor = 0
			return m, nil
		}
		m.projectPicker = false

	case key.Matches(msg, common.Keys.Up):
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		if m.pickerCursor < len(items)-1 {
			m.pickerCursor++
		}

	case key.Matches(msg, common.Keys.Enter):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			for i, p := range m.projects {
				if p == slug {
					m.activeProject = i
					break
				}
			}
			m.projectPicker = false
			m.pickerFilter = ""
			m.reload()
		}

	case key.Matches(msg, common.Keys.Space):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			if m.hiddenProjects[slug] {
				delete(m.hiddenProjects, slug)
			} else {
				m.hiddenProjects[slug] = true
			}
			for i := range m.pickerItems {
				if m.pickerItems[i].slug == slug {
					m.pickerItems[i].hidden = m.hiddenProjects[slug]
				}
			}
			m.saveTUIState()
			m.sortPickerItems()
			filtered := m.filteredPickerItems()
			if m.pickerCursor >= len(filtered) {
				m.pickerCursor = max(0, len(filtered)-1)
			}
		}

	case key.Matches(msg, common.Keys.ReorderDown):
		if m.pickerFilter == "" && m.pickerCursor < len(m.pickerItems)-1 {
			m.pickerReorder(1)
			m.saveTUIState()
		}

	case key.Matches(msg, common.Keys.ReorderUp):
		if m.pickerFilter == "" && m.pickerCursor > 0 {
			m.pickerReorder(-1)
			m.saveTUIState()
		}

	case key.Matches(msg, common.Keys.Claude):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			m.projectPicker = false
			m.pickerFilter = ""
			m.openProjectClaudeMenu(slug)
		}

	case key.Matches(msg, common.Keys.ProjectInfo):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			proj, _ := m.store.GetProject(slug)
			if proj != nil {
				m.projectPicker = false
				m.pickerFilter = ""
				m.previousView = viewBoard
				m.currentView = viewProjectInfo
				m.infoProject = proj
				m.infoSlug = slug
				m.detailViewport = viewport.New(m.width, m.height-2)
				m.detailViewport.SetContent(renderProjectInfo(proj, slug, m.width))
			}
		}

	case key.Matches(msg, common.Keys.Yank):
		if m.pickerCursor < len(items) {
			item := items[m.pickerCursor]
			if item.path != "" {
				m.copyToClipboard(item.path)
			} else {
				m.copyToClipboard(item.slug)
			}
		}

	case key.Matches(msg, common.Keys.YankMenu):
		if m.pickerCursor < len(items) {
			item := items[m.pickerCursor]
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"slug", item.slug})
			m.yankItems = append(m.yankItems, yankItem{"name", item.name})
			if item.path != "" {
				m.yankItems = append(m.yankItems, yankItem{"path", item.path})
			}
			if item.repo != "" {
				m.yankItems = append(m.yankItems, yankItem{"repo", item.repo})
			}
			if item.stack != "" {
				m.yankItems = append(m.yankItems, yankItem{"stack", item.stack})
			}
			for name, url := range item.links {
				m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
			}
			if len(item.tags) > 0 {
				m.yankItems = append(m.yankItems, yankItem{"tags", strings.Join(item.tags, ", ")})
			}
			if len(m.yankItems) > 0 {
				m.yankMenu = true
			}
		}

	case key.Matches(msg, common.Keys.Search):
		m.pickerInput.SetValue(m.pickerFilter)
		m.pickerInput.Focus()
		return m, m.pickerInput.Cursor.BlinkCmd()
	}

	return m, nil
}

func (m *Model) pickerReorder(dir int) {
	target := m.pickerCursor + dir
	if target < 0 || target >= len(m.pickerItems) {
		return
	}
	// don't swap across hidden/visible boundary
	if m.pickerItems[m.pickerCursor].hidden != m.pickerItems[target].hidden {
		return
	}
	// swap in picker
	m.pickerItems[m.pickerCursor], m.pickerItems[target] = m.pickerItems[target], m.pickerItems[m.pickerCursor]
	m.pickerCursor = target

	// rebuild m.projects from picker order (visible first, then hidden - matching picker)
	newProjects := []string{"all"}
	for _, item := range m.pickerItems {
		newProjects = append(newProjects, item.slug)
	}
	// preserve activeProject by slug
	activeSlug := ""
	if m.activeProject > 0 && m.activeProject < len(m.projects) {
		activeSlug = m.projects[m.activeProject]
	}
	m.projects = newProjects
	if activeSlug != "" {
		for i, p := range m.projects {
			if p == activeSlug {
				m.activeProject = i
				break
			}
		}
	}
}

// openSubtaskPicker opens the overlay listing a tracker's children for
// arrow-key selection (used when pressing p on a parent task in the detail view).
func (m *Model) openSubtaskPicker(kids []*storage.Task) {
	m.subtaskItems = kids
	m.subtaskCursor = 0
	m.subtaskPicker = true
}

func (m Model) updateSubtaskPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.subtaskPicker = false
	case key.Matches(msg, common.Keys.Up):
		if m.subtaskCursor > 0 {
			m.subtaskCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.subtaskCursor < len(m.subtaskItems)-1 {
			m.subtaskCursor++
		}
	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Right):
		if m.subtaskCursor < len(m.subtaskItems) {
			target := m.subtaskItems[m.subtaskCursor]
			m.subtaskPicker = false
			m.openDetailTask(target)
		}
	}
	return m, nil
}
