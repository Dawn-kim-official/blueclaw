package tui

import lipgloss "charm.land/lipgloss/v2"

var (
	styleHeaderBar    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")).Padding(0, 1)
	styleTabActive    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("24")).Padding(0, 1)
	styleTabIdle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	styleFooter       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleError        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	styleWarning      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleMuted        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleSelected     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("24"))
	styleSuccess      = lipgloss.NewStyle().Foreground(lipgloss.Color("41"))
	styleFailure      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleSectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
)

func statusStyle(status string) lipgloss.Style {
	switch status {
	case TaskStatusCompleted:
		return styleSuccess
	case TaskStatusFailed, TaskStatusCancelled:
		return styleFailure
	case TaskStatusWaitingApproval, TaskStatusWaitingInput, TaskStatusBlocked:
		return styleWarning
	default:
		return lipgloss.NewStyle()
	}
}
