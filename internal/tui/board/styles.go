package board

import "github.com/charmbracelet/lipgloss"

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}

	columnStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle)

	activeColumnStyle = lipgloss.NewStyle().
				Padding(1, 2).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(highlight)

	columnHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(highlight).
				MarginBottom(1)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			Padding(0, 1).
			MarginBottom(1)

	activeCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(0, 1).
			MarginBottom(1)

	cardTitleStyle = lipgloss.NewStyle().
			Bold(true)

	cardProjectStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

	cardTagStyle = lipgloss.NewStyle().
			Foreground(special)

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2)

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(highlight)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			Padding(0, 0, 1, 0)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#555555"})

	toastStyle = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
			Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
			Padding(0, 1)
)
