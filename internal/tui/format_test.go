package tui

import (
	"testing"
	"time"
)

func TestTruncateTextLeavesShortTextUntouched(testInstance *testing.T) {
	result := truncateText("summarize report", 40)
	if result != "summarize report" {
		testInstance.Fatalf("unexpected result: %q", result)
	}
}

func TestTruncateTextCollapsesWhitespaceAndAddsEllipsis(testInstance *testing.T) {
	result := truncateText("summarize   the\nquarterly   report for the board", 20)
	if result != "summarize the quart…" {
		testInstance.Fatalf("unexpected result: %q", result)
	}
	if len([]rune(result)) != 20 {
		testInstance.Fatalf("expected exactly 20 runes, got %d: %q", len([]rune(result)), result)
	}
}

func TestFormatAgeBuckets(testInstance *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name      string
		timestamp time.Time
		expected  string
	}{
		{"zero value", time.Time{}, "-"},
		{"seconds", now.Add(-30 * time.Second), "30s"},
		{"minutes", now.Add(-5 * time.Minute), "5m"},
		{"hours", now.Add(-3 * time.Hour), "3h"},
		{"days", now.Add(-49 * time.Hour), "2d"},
		{"future clamps to zero", now.Add(5 * time.Minute), "0s"},
	}
	for _, testCase := range testCases {
		testInstance.Run(testCase.name, func(testInstance *testing.T) {
			result := formatAge(testCase.timestamp, now)
			if result != testCase.expected {
				testInstance.Fatalf("expected %q, got %q", testCase.expected, result)
			}
		})
	}
}
