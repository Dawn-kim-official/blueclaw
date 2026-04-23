package integration

import (
	"testing"

	"blueclaw/internal/agent"
)

func TestTaskPlanCompile(t *testing.T) {
	planCompiler := agent.PlanCompiler{}
	taskPlan, errorValue := planCompiler.CompilePlan("research payroll policy")
	if errorValue != nil {
		t.Fatalf("expected task plan to compile: %v", errorValue)
	}
	if len(taskPlan.TaskSteps) != 3 {
		t.Fatalf("expected 3 task steps, got %d", len(taskPlan.TaskSteps))
	}
}
