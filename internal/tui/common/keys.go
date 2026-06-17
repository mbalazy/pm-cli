package common

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up            key.Binding
	Down          key.Binding
	Left          key.Binding
	Right         key.Binding
	Enter         key.Binding
	Open          key.Binding
	Space         key.Binding
	Move          key.Binding
	MoveBack      key.Binding
	Done          key.Binding
	Add           key.Binding
	Edit          key.Binding
	Links         key.Binding
	Tab           key.Binding
	ShiftTab      key.Binding
	Search        key.Binding
	Quit          key.Binding
	Escape        key.Binding
	Help          key.Binding
	Delete        key.Binding
	JumpTop       key.Binding
	JumpBottom    key.Binding
	Yank          key.Binding
	YankMenu      key.Binding
	Archive       key.Binding
	ToggleArchive key.Binding
	Restore       key.Binding
	ProjectInfo   key.Binding
	Undo          key.Binding
	Waiting       key.Binding
	Select        key.Binding
	ColumnVis     key.Binding
	HalfDown      key.Binding
	HalfUp        key.Binding
	Zoom          key.Binding
	ReorderUp     key.Binding
	ReorderDown   key.Binding
	Claude        key.Binding
	Executor      key.Binding
	WatchExecutor key.Binding
	KillRun       key.Binding
	ProjectPicker key.Binding
	Focus         key.Binding
	FocusView     key.Binding
}

var Keys = KeyMap{
	Up:            key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:          key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Left:          key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "prev column")),
	Right:         key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "next column")),
	Enter:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
	Open:          key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open/close")),
	Space:         key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
	Move:          key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "move forward")),
	MoveBack:      key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "move back")),
	Done:          key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "mark done")),
	Add:           key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add task")),
	Edit:          key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit in $EDITOR")),
	Links:         key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "open links")),
	Tab:           key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next project")),
	ShiftTab:      key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("S-tab", "prev project")),
	Search:        key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Quit:          key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Escape:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Help:          key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Delete:        key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete task")),
	JumpTop:       key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "jump to top")),
	JumpBottom:    key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "jump to bottom")),
	Yank:          key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yank branch")),
	YankMenu:      key.NewBinding(key.WithKeys("Y"), key.WithHelp("Y", "yank menu")),
	Archive:       key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "archive task")),
	ToggleArchive: key.NewBinding(key.WithKeys("ctrl+a"), key.WithHelp("C-a", "toggle archive")),
	Restore:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "unarchive")),
	ProjectInfo:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "project info")),
	Undo:          key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "undo")),
	Waiting:       key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "mark waiting")),
	Select:        key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "select mode")),
	ColumnVis:     key.NewBinding(key.WithKeys("V"), key.WithHelp("V", "toggle columns")),
	HalfDown:      key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("C-d", "half page down")),
	HalfUp:        key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("C-u", "half page up")),
	Zoom:          key.NewBinding(key.WithKeys(";"), key.WithHelp(";", "zoom toggle")),
	ReorderUp:     key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("C-k", "reorder up")),
	ReorderDown:   key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("C-j", "reorder down")),
	Claude:        key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "claude code")),
	Executor:      key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "run executor")),
	WatchExecutor: key.NewBinding(key.WithKeys("W"), key.WithHelp("W", "watch executor run")),
	KillRun:       key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "kill executor run")),
	ProjectPicker: key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "project picker")),
	Focus:         key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "focus")),
	FocusView:     key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "focus view")),
}
