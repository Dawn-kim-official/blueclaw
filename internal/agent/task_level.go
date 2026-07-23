package agent

import (
	"strings"
	"time"
)

type TaskLevel string

const (
	TaskLevelXLow   TaskLevel = "xlow"
	TaskLevelLow    TaskLevel = "low"
	TaskLevelMedium TaskLevel = "medium"
	TaskLevelHigh   TaskLevel = "high"
	TaskLevelXHigh  TaskLevel = "xhigh"
	TaskLevelMax    TaskLevel = "max"
)

type TaskLevelProfile struct {
	TaskLevel         TaskLevel
	Duration          time.Duration
	MaxIterationCount int
	MaxToolCallCount  int
}

var taskLevelProfiles = []TaskLevelProfile{
	{TaskLevel: TaskLevelXLow, Duration: 3 * time.Minute, MaxIterationCount: 4, MaxToolCallCount: 1},
	{TaskLevel: TaskLevelLow, Duration: 10 * time.Minute, MaxIterationCount: 40, MaxToolCallCount: 30},
	{TaskLevel: TaskLevelMedium, Duration: 20 * time.Minute, MaxIterationCount: 180, MaxToolCallCount: 100},
	{TaskLevel: TaskLevelHigh, Duration: 40 * time.Minute, MaxIterationCount: 400, MaxToolCallCount: 220},
	{TaskLevel: TaskLevelXHigh, Duration: time.Hour, MaxIterationCount: 500, MaxToolCallCount: 260},
	{TaskLevel: TaskLevelMax, Duration: time.Hour, MaxIterationCount: 700, MaxToolCallCount: 340},
}

func taskLevelRank(taskLevel TaskLevel) int {
	normalizedTaskLevel := NormalizeTaskLevel(string(taskLevel))
	for index, taskLevelProfile := range taskLevelProfiles {
		if taskLevelProfile.TaskLevel == normalizedTaskLevel {
			return index
		}
	}
	return -1
}

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

func TaskLevelProfileForLevel(taskLevel TaskLevel) TaskLevelProfile {
	normalizedTaskLevel := NormalizeTaskLevel(string(taskLevel))
	if normalizedTaskLevel == "" {
		normalizedTaskLevel = TaskLevelLow
	}
	for _, taskLevelProfile := range taskLevelProfiles {
		if taskLevelProfile.TaskLevel == normalizedTaskLevel {
			return taskLevelProfile
		}
	}
	return taskLevelProfiles[1]
}

func LargerTaskLevel(first TaskLevel, second TaskLevel) TaskLevel {
	if taskLevelRank(second) > taskLevelRank(first) {
		return second
	}
	return first
}

func nextTaskLevel(taskLevel TaskLevel) (TaskLevel, bool) {
	currentRank := taskLevelRank(TaskLevelProfileForLevel(taskLevel).TaskLevel)
	if currentRank < 0 || currentRank+1 >= len(taskLevelProfiles) {
		return "", false
	}
	return taskLevelProfiles[currentRank+1].TaskLevel, true
}

func taskLevelWantsProgressCheckpoints(taskLevel TaskLevel) bool {
	return taskLevelRank(taskLevel) >= taskLevelRank(TaskLevelMedium)
}

func taskLevelWantsSingleFinalReply(taskLevel TaskLevel) bool {
	return NormalizeTaskLevel(string(taskLevel)) == TaskLevelXLow
}

func taskLevelRequiresPlan(taskLevel TaskLevel) bool {
	return taskLevelRank(taskLevel) >= taskLevelRank(TaskLevelMedium)
}
