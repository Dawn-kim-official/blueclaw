package tui

import (
	"regexp"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func tasksScreenWithMixedStatusWidths() string {
	model := Model{
		screen:      screenTasks,
		width:       120,
		tasksCursor: 0,
		taskRuns: []TaskRun{
			{TaskRunID: "tr_9f2a", Status: TaskStatusWaitingApproval, RequesterDisplayName: "Ada", Prompt: "Send the summary"},
			{TaskRunID: "tr_7c14", Status: TaskStatusRunning, RequesterDisplayName: "Grace", Prompt: "Draft the review"},
			{TaskRunID: "tr_20a7", Status: TaskStatusFailed, RequesterDisplayName: "Rob", Prompt: "Reconcile invoices"},
		},
	}
	return model.renderTasksScreen()
}

func TestTaskTableKeepsEveryRowTheSameWidthAcrossStatusLengths(testInstance *testing.T) {
	lines := strings.Split(tasksScreenWithMixedStatusWidths(), "\n")

	widthByLine := map[int][]string{}
	for _, line := range lines[1:] {
		widthByLine[lipgloss.Width(line)] = append(widthByLine[lipgloss.Width(line)], line)
	}
	if len(widthByLine) != 1 {
		testInstance.Fatalf("expected every table line to share one width, got %d distinct widths: %v", len(widthByLine), widthByLine)
	}
}

func TestSelectedTaskRowHighlightSurvivesToTheEndOfTheRow(testInstance *testing.T) {
	lines := strings.Split(tasksScreenWithMixedStatusWidths(), "\n")

	selectedLine := ""
	for _, line := range lines {
		if strings.Contains(line, "tr_9f2a") {
			selectedLine = line
		}
	}
	if selectedLine == "" {
		testInstance.Fatal("expected the selected task run to be rendered")
	}

	if unhighlighted := textRunsWithoutBackground(selectedLine, "48;2;46;125;196"); len(unhighlighted) > 0 {
		testInstance.Fatalf("the selection highlight breaks mid-row; these runs carry no background: %q in %q", unhighlighted, selectedLine)
	}
}

func textRunsWithoutBackground(line string, backgroundCode string) []string {
	escape := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	runs := escape.Split(line, -1)
	styles := escape.FindAllString(line, -1)

	unhighlighted := []string{}
	hasBackground := false
	for runIndex, run := range runs {
		trimmed := strings.Trim(run, " │")
		if trimmed != "" && !hasBackground {
			unhighlighted = append(unhighlighted, run)
		}
		if runIndex < len(styles) {
			hasBackground = strings.Contains(styles[runIndex], backgroundCode)
		}
	}
	return unhighlighted
}
