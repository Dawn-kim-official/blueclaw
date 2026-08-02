package toolcontract

import "strings"

const maximumPlanStepCount = 12

type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

var planStepStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"done":        true,
	"skipped":     true,
}

func NormalizePlan(goal string, steps []PlanStep) (string, []PlanStep) {
	return truncateText(compactWhitespace(goal), 300), NormalizePlanSteps(steps)
}

func NormalizePlanSteps(steps []PlanStep) []PlanStep {
	normalizedSteps := []PlanStep{}
	for _, step := range steps {
		title := truncateText(compactWhitespace(step.Title), 200)
		status := strings.TrimSpace(step.Status)
		if title == "" {
			continue
		}
		if !planStepStatuses[status] {
			status = "pending"
		}
		normalizedSteps = append(normalizedSteps, PlanStep{Title: title, Status: status})
		if len(normalizedSteps) >= maximumPlanStepCount {
			break
		}
	}
	return normalizedSteps
}
