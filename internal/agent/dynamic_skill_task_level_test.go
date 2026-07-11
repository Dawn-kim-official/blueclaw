package agent

import (
	"context"
	"encoding/json"
	"testing"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestDynamicVisualSkillSelectionPromotesCurrentTurnToXHigh(t *testing.T) {
	lowLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"skill.search","toolInput":{"queries":[{"description":"website creation"}]}}`,
		`{"action":"skill.select","skillNames":["website"],"reason":"search result matches"}`,
	}}
	xHighLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.create","toolInput":{"slug":"demo"}}`,
		finishMessageWithEvidence("created", "obs-003", "site.create", 0),
	}}
	services := newTurnRunnerTestServices(lowLanguageModel, TurnOptions{
		TaskLevel:         TaskLevelLow,
		MaxIterationCount: 10,
		MaxToolCallCount:  10,
	})
	services.runner.UseTaskLanguageModelResolver(func(taskLevel TaskLevel) llm.LanguageModelProvider {
		if taskLevel == TaskLevelXHigh {
			return xHighLanguageModel
		}
		return lowLanguageModel
	})
	toolRegistry := NewToolSet([]string{SkillSearchToolName})
	toolRegistry.RegisterTool(ToolDefinition{Name: SkillSearchToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[{"name":"website"}]}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{
		Name:        "site.create",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"siteID":"site-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "create website",
		TaskLevel:         TaskLevelLow,
		ToolSet:           toolRegistry,
		AvailableSkills: []SkillInstruction{{
			Name:         "website",
			AllowedTools: []string{"site.create"},
		}},
	})

	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "created" {
		t.Fatalf("expected xhigh model to finish the turn, got %q", result.FinishMessage)
	}
	if len(lowLanguageModel.requests) != 2 || len(xHighLanguageModel.requests) != 2 {
		t.Fatalf("expected model switch after skill.select, low=%d xhigh=%d", len(lowLanguageModel.requests), len(xHighLanguageModel.requests))
	}
	xHighProfile := TaskLevelProfileForLevel(TaskLevelXHigh)
	if services.runner.options.TaskLevel != TaskLevelXHigh || services.runner.options.MaxIterationCount < xHighProfile.MaxIterationCount || services.runner.options.MaxToolCallCount < xHighProfile.MaxToolCallCount {
		t.Fatalf("expected xhigh floor, got %+v", services.runner.options)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.skill_task_level_escalated", `"skillNames":["website"]`) || !taskEventsContain(taskEvents, "agent.skill_task_level_escalated", `"newTaskLevel":"xhigh"`) {
		t.Fatal("expected durable website xhigh escalation event")
	}
	if !taskEventsContain(taskEvents, "llm.call", `"modelTier":"xhigh"`) {
		t.Fatal("expected post-selection model calls to use the xhigh tier")
	}
}

func TestVisualSkillTaskLevelFloorUsesExactCurrentSkillNames(t *testing.T) {
	for _, skillName := range []string{"presentation", "website"} {
		if taskLevelFloorForSelectedSkillNames([]string{skillName}) != TaskLevelXHigh {
			t.Fatalf("expected %s to floor at xhigh", skillName)
		}
	}
	for _, skillName := range []string{"website-management", "scheduled-task", "document"} {
		if taskLevelFloorForSelectedSkillNames([]string{skillName}) != "" {
			t.Fatalf("did not expect %s to have a programmed task-level floor", skillName)
		}
	}
}

func TestVisualSkillNameFloorDoesNotRequireCompletionMetadata(t *testing.T) {
	instructionBundle := namedSkillBundle("presentation")
	instructionBundle.Skills[0].Completion = SkillCompletion{}
	promotedDecision := promoteIntakeDecisionForSelectedSkills(
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow},
		instructionBundle,
		IntakeOptions{},
	)
	if promotedDecision.TaskLevel != TaskLevelXHigh {
		t.Fatalf("expected presentation name to floor at xhigh without completion metadata, got %q", promotedDecision.TaskLevel)
	}
}

func TestVisualSkillTaskLevelEscalationRestoresOnContinuation(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		Name: "agent.skill_task_level_escalated",
		Body: `{"previousTaskLevel":"low","newTaskLevel":"xhigh","skillNames":["presentation"]}`,
	}}
	if highestEscalatedTaskLevel(taskEvents) != TaskLevelXHigh {
		t.Fatal("expected dynamic visual skill floor to survive task continuation")
	}
}
