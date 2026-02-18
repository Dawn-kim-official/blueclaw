package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	z "github.com/Oudwins/zog"
	zjson "github.com/Oudwins/zog/parsers/zjson"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type TaskPlan struct {
	Acknowledgment string           `json:"acknowledgment"`
	Achievement    *AchievementPlan `json:"achievement"`
}

type AchievementPlan struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	EtaMinutes  int      `json:"etaMinutes"`
	Plan        []string `json:"plan"`
}

var achievementPlanSchema = z.Struct(z.Shape{
	"name":        z.String().Min(1),
	"description": z.String().Min(1),
	"etaMinutes":  z.Int().GTE(1).Default(5),
	"plan":        z.Slice(z.String().Min(1)).Min(1),
})

var taskPlanSchema = z.Struct(z.Shape{
	"acknowledgment": z.String(),
	"achievement":    z.Ptr(achievementPlanSchema),
})

var taskPlanExample = TaskPlan{
	Acknowledgment: "I'll research Indian culture and write a comprehensive markdown file.",
	Achievement: &AchievementPlan{
		Name:        "indian-culture-research",
		Description: "Research and summarize Indian culture in a markdown file",
		EtaMinutes:  5,
		Plan:        []string{"Gather information", "Organize topics", "Write file"},
	},
}

var systemPrompt = buildSystemPrompt()

func buildSystemPrompt() string {
	exampleJSON, _ := json.MarshalIndent(taskPlanExample, "", "  ")
	return "Analyze the user's request. Output ONLY valid JSON matching this schema:\n\n" +
		string(exampleJSON) +
		"\n\nSet achievement to null for heartbeat check-ins, simple questions, or single-step tasks." +
		"\nSet achievement to an object when 2+ distinct steps are needed."
}

func PlanTask(requestContext context.Context, llmProvider provider.LLMProvider, messages []provider.Message, model string) (TaskPlan, error) {
	response, err := llmProvider.SendMessage(requestContext, provider.Request{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Model:        model,
		JSONMode:     true,
	})
	if err != nil {
		return TaskPlan{}, fmt.Errorf("planning LLM call: %w", err)
	}
	var plan TaskPlan
	issues := taskPlanSchema.Parse(zjson.Decode(strings.NewReader(response.Message.Content)), &plan)
	if issues != nil {
		log.Printf("warning: planning validation failed: %v", issues)
		return TaskPlan{}, nil
	}
	return plan, nil
}

func CreateTaskFromPlan(achievementPlan AchievementPlan, tasksDirectory string) error {
	planSteps := make([]any, len(achievementPlan.Plan))
	for index, step := range achievementPlan.Plan {
		planSteps[index] = step
	}
	taskTool := tool.NewCreateTaskTool(tasksDirectory)
	result, err := taskTool.Execute(context.Background(), map[string]any{
		"name":        achievementPlan.Name,
		"description": achievementPlan.Description,
		"eta_minutes": achievementPlan.EtaMinutes,
		"plan":        planSteps,
	})
	if err != nil {
		return fmt.Errorf("creating task: %w", err)
	}
	if result.Error != "" {
		return fmt.Errorf("task tool error: %s", result.Error)
	}
	return nil
}
