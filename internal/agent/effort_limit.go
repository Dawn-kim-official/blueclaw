package agent

import (
	"strings"
	"time"
)

type EffortLevel string

const (
	EffortLevelQuick    EffortLevel = "quick"
	EffortLevelStandard EffortLevel = "standard"
	EffortLevelDeep     EffortLevel = "deep"
	EffortLevelExtended EffortLevel = "extended"
)

type EffortLimitProfile struct {
	EffortLevel       EffortLevel
	Duration          time.Duration
	MaxIterationCount int
	MaxToolCallCount  int
}

var effortLimitProfiles = []EffortLimitProfile{
	{EffortLevel: EffortLevelQuick, Duration: time.Minute, MaxIterationCount: 4, MaxToolCallCount: 1},
	{EffortLevel: EffortLevelStandard, Duration: 10 * time.Minute, MaxIterationCount: 24, MaxToolCallCount: 8},
	{EffortLevel: EffortLevelDeep, Duration: 30 * time.Minute, MaxIterationCount: 90, MaxToolCallCount: 32},
	{EffortLevel: EffortLevelExtended, Duration: 2 * time.Hour, MaxIterationCount: 140, MaxToolCallCount: 64},
}

func NormalizeEffortLevel(value string) EffortLevel {
	switch EffortLevel(strings.TrimSpace(value)) {
	case EffortLevelQuick:
		return EffortLevelQuick
	case EffortLevelStandard:
		return EffortLevelStandard
	case EffortLevelDeep:
		return EffortLevelDeep
	case EffortLevelExtended:
		return EffortLevelExtended
	default:
		return ""
	}
}

func EffortLimitProfileForLevel(effortLevel EffortLevel) EffortLimitProfile {
	normalizedEffortLevel := NormalizeEffortLevel(string(effortLevel))
	if normalizedEffortLevel == "" {
		normalizedEffortLevel = EffortLevelStandard
	}
	for _, effortLimitProfile := range effortLimitProfiles {
		if effortLimitProfile.EffortLevel == normalizedEffortLevel {
			return effortLimitProfile
		}
	}
	return effortLimitProfiles[1]
}

func LargerEffortLevel(first EffortLevel, second EffortLevel) EffortLevel {
	if effortLevelRank(second) > effortLevelRank(first) {
		return second
	}
	return first
}

func effortLevelRank(effortLevel EffortLevel) int {
	normalizedEffortLevel := NormalizeEffortLevel(string(effortLevel))
	for index, effortLimitProfile := range effortLimitProfiles {
		if effortLimitProfile.EffortLevel == normalizedEffortLevel {
			return index
		}
	}
	return -1
}
