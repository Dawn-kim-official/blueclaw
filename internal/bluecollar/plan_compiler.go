package bluecollar

import (
	"errors"
	"strings"
)

type PlanCompiler struct{}

func (planCompiler PlanCompiler) CompilePlan(instruction string) (TaskPlan, error) {
	trimmedInstruction := strings.TrimSpace(instruction)
	if trimmedInstruction == "" {
		return TaskPlan{}, errors.New("instruction is required")
	}

	return TaskPlan{
		Instruction: trimmedInstruction,
		TaskSteps: []TaskPlanStep{
			{
				Name:                     "plan",
				AssignedAgentProfileName: "planner",
				Instruction:              trimmedInstruction,
			},
			{
				Name:                     "research",
				AssignedAgentProfileName: "researcher",
				Instruction:              trimmedInstruction,
			},
			{
				Name:                     "respond",
				AssignedAgentProfileName: "responder",
				Instruction:              trimmedInstruction,
			},
		},
	}, nil
}
