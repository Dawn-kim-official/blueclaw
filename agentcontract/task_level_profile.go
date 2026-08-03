package agentcontract

import (
	"time"
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

func NextTaskLevel(taskLevel TaskLevel) (TaskLevel, bool) {
	currentRank := TaskLevelRank(TaskLevelProfileForLevel(taskLevel).TaskLevel)
	if currentRank < 0 || currentRank+1 >= len(taskLevelProfiles) {
		return "", false
	}
	return taskLevelProfiles[currentRank+1].TaskLevel, true
}

func TaskLevelWantsProgressCheckpoints(taskLevel TaskLevel) bool {
	return TaskLevelRank(taskLevel) >= TaskLevelRank(TaskLevelMedium)
}

func TaskLevelWantsSingleFinalReply(taskLevel TaskLevel) bool {
	return NormalizeTaskLevel(string(taskLevel)) == TaskLevelXLow
}

func TaskLevelRequiresPlan(taskLevel TaskLevel) bool {
	return TaskLevelRank(taskLevel) >= TaskLevelRank(TaskLevelMedium)
}
