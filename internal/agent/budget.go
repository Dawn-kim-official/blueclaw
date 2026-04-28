package agent

import (
	"strings"
	"time"
)

type BudgetClass string

const (
	BudgetClassFiveMinutes   BudgetClass = "five_minutes"
	BudgetClassTenMinutes    BudgetClass = "ten_minutes"
	BudgetClassThirtyMinutes BudgetClass = "thirty_minutes"
	BudgetClassOneHour       BudgetClass = "one_hour"
	BudgetClassSixHours      BudgetClass = "six_hours"
	BudgetClassHalfDay       BudgetClass = "half_day"
)

type BudgetProfile struct {
	BudgetClass   BudgetClass
	Duration      time.Duration
	MaxIterations int
	MaxToolCalls  int
}

var budgetProfiles = []BudgetProfile{
	{BudgetClass: BudgetClassFiveMinutes, Duration: 5 * time.Minute, MaxIterations: 16, MaxToolCalls: 24},
	{BudgetClass: BudgetClassTenMinutes, Duration: 10 * time.Minute, MaxIterations: 32, MaxToolCalls: 48},
	{BudgetClass: BudgetClassThirtyMinutes, Duration: 30 * time.Minute, MaxIterations: 96, MaxToolCalls: 144},
	{BudgetClass: BudgetClassOneHour, Duration: time.Hour, MaxIterations: 192, MaxToolCalls: 288},
	{BudgetClass: BudgetClassSixHours, Duration: 6 * time.Hour, MaxIterations: 512, MaxToolCalls: 1024},
	{BudgetClass: BudgetClassHalfDay, Duration: 12 * time.Hour, MaxIterations: 1024, MaxToolCalls: 2048},
}

func NormalizeBudgetClass(value string) BudgetClass {
	switch BudgetClass(strings.TrimSpace(value)) {
	case BudgetClassFiveMinutes:
		return BudgetClassFiveMinutes
	case BudgetClassTenMinutes:
		return BudgetClassTenMinutes
	case BudgetClassThirtyMinutes:
		return BudgetClassThirtyMinutes
	case BudgetClassOneHour:
		return BudgetClassOneHour
	case BudgetClassSixHours:
		return BudgetClassSixHours
	case BudgetClassHalfDay:
		return BudgetClassHalfDay
	default:
		return ""
	}
}

func BudgetProfileForClass(budgetClass BudgetClass) BudgetProfile {
	normalizedBudgetClass := NormalizeBudgetClass(string(budgetClass))
	if normalizedBudgetClass == "" {
		normalizedBudgetClass = BudgetClassThirtyMinutes
	}
	for _, budgetProfile := range budgetProfiles {
		if budgetProfile.BudgetClass == normalizedBudgetClass {
			return budgetProfile
		}
	}
	return budgetProfiles[2]
}

func LargerBudgetClass(first BudgetClass, second BudgetClass) BudgetClass {
	if budgetClassRank(second) > budgetClassRank(first) {
		return second
	}
	return first
}

func budgetClassRank(budgetClass BudgetClass) int {
	normalizedBudgetClass := NormalizeBudgetClass(string(budgetClass))
	for index, budgetProfile := range budgetProfiles {
		if budgetProfile.BudgetClass == normalizedBudgetClass {
			return index
		}
	}
	return -1
}

func BudgetClassLabel(budgetClass BudgetClass) string {
	switch NormalizeBudgetClass(string(budgetClass)) {
	case BudgetClassFiveMinutes:
		return "five-minute"
	case BudgetClassTenMinutes:
		return "ten-minute"
	case BudgetClassThirtyMinutes:
		return "thirty-minute"
	case BudgetClassOneHour:
		return "one-hour"
	case BudgetClassSixHours:
		return "six-hour"
	case BudgetClassHalfDay:
		return "half-day"
	default:
		return "thirty-minute"
	}
}

func BudgetClassKoreanLabel(budgetClass BudgetClass) string {
	switch NormalizeBudgetClass(string(budgetClass)) {
	case BudgetClassFiveMinutes:
		return "5분"
	case BudgetClassTenMinutes:
		return "10분"
	case BudgetClassThirtyMinutes:
		return "30분"
	case BudgetClassOneHour:
		return "1시간"
	case BudgetClassSixHours:
		return "6시간"
	case BudgetClassHalfDay:
		return "반나절"
	default:
		return "30분"
	}
}
