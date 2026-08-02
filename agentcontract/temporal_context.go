package agentcontract

import (
	"strings"
	"time"
)

func BuildTemporalContextDescription(turnStartedAt time.Time) string {
	currentTime := turnStartedAt
	if currentTime.IsZero() {
		currentTime = time.Now()
	}
	location := TemporalContextLocation()
	localTime := currentTime.In(location)
	return strings.Join([]string{
		"Runtime temporal context:",
		"Current date: " + localTime.Format("2006-01-02"),
		"Current weekday: " + localTime.Weekday().String(),
		"Current time: " + localTime.Format("15:04"),
		"Time zone: " + location.String(),
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func buildCurrentWeekDescription(localTime time.Time) string {
	daysSinceMonday := (int(localTime.Weekday()) + 6) % 7
	monday := localTime.AddDate(0, 0, -daysSinceMonday)
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	anchors := make([]string, 0, len(weekdays))
	for dayOffset, weekday := range weekdays {
		anchors = append(anchors, weekday+"="+monday.AddDate(0, 0, dayOffset).Format("2006-01-02"))
	}
	return "Current week: " + strings.Join(anchors, ", ")
}

func TemporalContextLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}
