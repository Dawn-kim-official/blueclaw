package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (setupModel SetupModel) Init() tea.Cmd {
	return nil
}

func (setupModel SetupModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	keyPressMessage, isKeyPress := message.(tea.KeyPressMsg)
	if !isKeyPress {
		return setupModel, nil
	}
	switch keyPressMessage.String() {
	case "ctrl+c":
		return setupModel, tea.Quit
	case "up":
		setupModel.cursor = clampInt(setupModel.cursor-1, 0, len(setupFieldOrder)-1)
	case "down", "tab":
		setupModel.cursor = clampInt(setupModel.cursor+1, 0, len(setupFieldOrder)-1)
	case "left", "right", " ":
		(&setupModel).cycleSelectedChoice()
	case "backspace":
		(&setupModel).deleteFromSelectedField()
	case "enter":
		if errorValue := (&setupModel).Finish(); errorValue == nil {
			return setupModel, tea.Quit
		}
	default:
		if text := keyPressMessage.String(); len(text) == 1 {
			(&setupModel).appendToSelectedField(text)
		}
	}
	return setupModel, nil
}

func (setupModel SetupModel) View() tea.View {
	lines := []string{
		styleHeaderBar.Render(" blueclaw setup "),
		"",
		styleMuted.Render("Nothing is configured yet. Answer these and blueclaw writes its own configuration to " + setupModel.home.DirectoryPath + "."),
		"",
	}
	for fieldIndex, fieldID := range setupFieldOrder {
		label := setupModel.fieldLabel(fieldID)
		value := setupModel.fieldValue(fieldID)
		if value == "" {
			value = styleMuted.Render("(empty)")
		}
		line := "  " + padFieldLabel(label) + value
		if fieldIndex == setupModel.cursor {
			line = styleSelected.Render("> " + padFieldLabel(label) + setupModel.fieldValue(fieldID))
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, setupModel.renderCheckResults()...)
	if setupModel.failureNotice != "" {
		lines = append(lines, styleError.Render(setupModel.failureNotice), "")
	}
	lines = append(lines, styleFooter.Render("up/down move · type to edit · space changes a choice · enter checks and finishes · ctrl+c quits"))
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func (setupModel SetupModel) renderCheckResults() []string {
	if len(setupModel.checkResults) == 0 {
		return []string{styleMuted.Render("  Press enter and blueclaw checks it can actually reach these before finishing."), ""}
	}
	lines := []string{styleSectionTitle.Render("  Checks")}
	for _, checkResult := range setupModel.checkResults {
		marker := styleFailure.Render("✗")
		detail := checkResult.Guidance
		if checkResult.IsReady {
			marker = styleSuccess.Render("✓")
			detail = checkResult.Detail
		}
		lines = append(lines, "  "+marker+" "+padFieldLabel(string(checkResult.Name))+styleMuted.Render(detail))
		if !checkResult.IsReady && checkResult.Detail != "" {
			lines = append(lines, "    "+styleMuted.Render(checkResult.Detail))
		}
	}
	return append(lines, "")
}

func padFieldLabel(label string) string {
	const labelWidth = 18
	if len(label) >= labelWidth {
		return label + " "
	}
	return label + strings.Repeat(" ", labelWidth-len(label))
}
