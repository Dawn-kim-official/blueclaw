package tui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

var (
	colorBrand     = lipgloss.Color("#2E7DC4")
	colorBrandDeep = lipgloss.Color("#1B4F80")
	colorBrandSoft = lipgloss.Color("#8FC3EE")
	colorInk       = lipgloss.Color("#F2F7FB")
	colorMuted     = lipgloss.Color("#7A8CA0")
	colorFaint     = lipgloss.Color("#4A5A6B")
	colorSurface   = lipgloss.Color("#2A3441")
	colorSuccess   = lipgloss.Color("#5FC98A")
	colorWarning   = lipgloss.Color("#E7B05A")
	colorFailure   = lipgloss.Color("#E8697D")
)

var (
	styleHeaderBar      = lipgloss.NewStyle().Bold(true).Foreground(colorInk).Background(colorBrandDeep).Padding(0, 1)
	styleTabActive      = lipgloss.NewStyle().Bold(true).Foreground(colorInk).Background(colorBrand)
	styleTabIdle        = lipgloss.NewStyle().Foreground(colorMuted)
	styleTabIdleDigit   = lipgloss.NewStyle().Foreground(colorFaint)
	styleTabActiveDigit = lipgloss.NewStyle().Foreground(colorBrandSoft).Background(colorBrand)
	styleFooter         = lipgloss.NewStyle().Foreground(colorMuted).Background(colorSurface)
	styleStatusBadge    = lipgloss.NewStyle().Bold(true).Foreground(colorInk).Background(colorFailure)
	styleEmptySymbol    = lipgloss.NewStyle().Foreground(colorBrandDeep)
	styleAccent         = lipgloss.NewStyle().Foreground(colorBrandSoft)
	styleError          = lipgloss.NewStyle().Bold(true).Foreground(colorFailure)
	styleWarning        = lipgloss.NewStyle().Foreground(colorWarning)
	styleMuted          = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected       = lipgloss.NewStyle().Bold(true).Foreground(colorInk).Background(colorBrand)
	styleTableCell      = lipgloss.NewStyle().Padding(0, 1)
	stylePanel          = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBrandDeep).Padding(0, 1)
	styleSuccess        = lipgloss.NewStyle().Foreground(colorSuccess)
	styleFailure        = lipgloss.NewStyle().Foreground(colorFailure)
	styleSectionTitle   = lipgloss.NewStyle().Bold(true).Foreground(colorBrandSoft)
	styleOK             = lipgloss.NewStyle().Foreground(colorSuccess)
)

func statusColor(status string) color.Color {
	switch status {
	case TaskStatusCompleted:
		return colorSuccess
	case TaskStatusFailed, TaskStatusCancelled:
		return colorFailure
	case TaskStatusWaitingApproval, TaskStatusWaitingInput, TaskStatusBlocked:
		return colorWarning
	default:
		return colorBrandSoft
	}
}

func statusStyle(status string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(statusColor(status))
}
