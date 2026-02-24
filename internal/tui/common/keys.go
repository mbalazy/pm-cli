package common

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Enter    key.Binding
	Move     key.Binding
	MoveBack key.Binding
	Add      key.Binding
	Edit     key.Binding
	Tab      key.Binding
	ShiftTab key.Binding
	Search   key.Binding
	Quit     key.Binding
	Escape   key.Binding
	Help     key.Binding
}

var Keys = KeyMap{
	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Left:   key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "prev column")),
	Right:  key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "next column")),
	Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
	Move:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "move forward")),
	MoveBack: key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "move back")),
	Add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add task")),
	Edit:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit in $EDITOR")),
	Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next project")),
	ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("S-tab", "prev project")),
	Search: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Quit:   key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Escape: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
}
