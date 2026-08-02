package agentcontract

import "strings"

type TaskLevel string

const (
	TaskLevelXLow   TaskLevel = "xlow"
	TaskLevelLow    TaskLevel = "low"
	TaskLevelMedium TaskLevel = "medium"
	TaskLevelHigh   TaskLevel = "high"
	TaskLevelXHigh  TaskLevel = "xhigh"
	TaskLevelMax    TaskLevel = "max"
)

func NormalizeTaskLevel(value string) TaskLevel {
	trimmedValue := strings.TrimSpace(value)
	switch TaskLevel(trimmedValue) {
	case TaskLevelXLow, TaskLevelLow, TaskLevelMedium, TaskLevelHigh, TaskLevelXHigh, TaskLevelMax:
		return TaskLevel(trimmedValue)
	default:
		return legacyTaskLevel(trimmedValue)
	}
}

func legacyTaskLevel(value string) TaskLevel {
	switch value {
	case "quick", "simple":
		return TaskLevelXLow
	case "standard", "normal":
		return TaskLevelLow
	case "deep", "complex":
		return TaskLevelMedium
	case "extended":
		return TaskLevelHigh
	default:
		return ""
	}
}
