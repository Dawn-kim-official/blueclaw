package agent

import (
	"encoding/json"
	"strings"

	"blueclaw/internal/task"
)

const nextStepPlanMaxTextCharacters = 360
const nextStepPlanMaxListItems = 8

func normalizeNextStepPlan(plan NextStepPlan) NextStepPlan {
	return NextStepPlan{
		Objective:           truncateText(compactWhitespace(plan.Objective), nextStepPlanMaxTextCharacters),
		ExpectedTools:       normalizeNextStepPlanList(plan.ExpectedTools),
		ExpectedNextResults: normalizeNextStepPlanList(plan.ExpectedNextResults),
		DoneCriteria:        normalizeNextStepPlanList(plan.DoneCriteria),
		Risk:                truncateText(compactWhitespace(plan.Risk), nextStepPlanMaxTextCharacters),
		WorkingSetReason:    truncateText(compactWhitespace(plan.WorkingSetReason), nextStepPlanMaxTextCharacters),
	}
}

func normalizeNextStepPlanList(values []string) []string {
	normalizedValues := []string{}
	seenValues := map[string]bool{}
	for _, value := range values {
		trimmedValue := truncateText(compactWhitespace(value), nextStepPlanMaxTextCharacters)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
		if len(normalizedValues) >= nextStepPlanMaxListItems {
			break
		}
	}
	return normalizedValues
}

func nextStepPlanFromTaskEvents(events []task.TaskEvent) NextStepPlan {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if strings.TrimSpace(event.Name) != "agent.next_step_plan" {
			continue
		}
		var plan NextStepPlan
		if json.Unmarshal([]byte(event.Body), &plan) == nil {
			return normalizeNextStepPlan(plan)
		}
	}
	return NextStepPlan{}
}

func nextStepPlanIsEmpty(plan NextStepPlan) bool {
	plan = normalizeNextStepPlan(plan)
	return plan.Objective == "" &&
		len(plan.ExpectedTools) == 0 &&
		len(plan.ExpectedNextResults) == 0 &&
		len(plan.DoneCriteria) == 0 &&
		plan.Risk == "" &&
		plan.WorkingSetReason == ""
}
