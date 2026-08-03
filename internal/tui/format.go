package tui

import (
	"fmt"
	"strings"
	"time"
)

// truncateText shortens text to at most maximumLength runes, appending an
// ellipsis when truncation happened. It collapses embedded newlines so list
// rows stay single-line.
func truncateText(text string, maximumLength int) string {
	collapsedText := strings.Join(strings.Fields(text), " ")
	runes := []rune(collapsedText)
	if len(runes) <= maximumLength {
		return collapsedText
	}
	if maximumLength <= 1 {
		return string(runes[:maximumLength])
	}
	return string(runes[:maximumLength-1]) + "…"
}

// formatAge renders how long ago a timestamp was, relative to now, in a
// short human-readable form such as "3m", "2h", or "5d".
func formatAge(timestamp time.Time, now time.Time) string {
	if timestamp.IsZero() {
		return "-"
	}
	elapsed := now.Sub(timestamp)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}
