package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
)

func invokePlanUpdateTool(t *testing.T, input string) json.RawMessage {
	t.Helper()
	toolRegistry := bluecollar.NewToolSet(nil)
	NewToolCatalogBuilder().registerPlanUpdateTool(toolRegistry)
	result, errorValue := toolRegistry.InvokeInternal(context.Background(), bluecollar.ToolInvocation{
		ToolName: bluecollar.PlanUpdateToolName,
		Input:    json.RawMessage(input),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected success, got %+v", result)
	}
	return result.Output.Data
}

func TestPlanUpdateToolEchoesNormalizedPlan(t *testing.T) {
	data := invokePlanUpdateTool(t, `{"goal":"  ship   the report ","steps":[{"title":"  gather   data ","status":"done"},{"title":"write summary","status":"in_progress"},{"title":"   ","status":"pending"}]}`)

	var output struct {
		Goal  string                `json:"goal"`
		Steps []bluecollar.PlanStep `json:"steps"`
	}
	if errorValue := json.Unmarshal(data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if output.Goal != "ship the report" {
		t.Fatalf("expected compacted goal, got %q", output.Goal)
	}
	if len(output.Steps) != 2 {
		t.Fatalf("expected the blank-title step dropped, got %+v", output.Steps)
	}
	if output.Steps[0].Title != "gather data" || output.Steps[0].Status != "done" {
		t.Fatalf("expected normalized first step, got %+v", output.Steps[0])
	}
	if output.Steps[1].Status != "in_progress" {
		t.Fatalf("expected preserved status, got %+v", output.Steps[1])
	}
}

func TestPlanUpdateToolRejectsUnknownStatusAtTheSchemaBoundary(t *testing.T) {
	toolRegistry := bluecollar.NewToolSet(nil)
	NewToolCatalogBuilder().registerPlanUpdateTool(toolRegistry)
	result, errorValue := toolRegistry.InvokeInternal(context.Background(), bluecollar.ToolInvocation{
		ToolName: bluecollar.PlanUpdateToolName,
		Input:    json.RawMessage(`{"steps":[{"title":"x","status":"weird"}]}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected an input schema failure, got %+v", result)
	}
}

func TestPlanUpdateToolAcceptsEmptyStepList(t *testing.T) {
	data := invokePlanUpdateTool(t, `{"steps":[]}`)
	if string(data) != `{"steps":[]}` {
		t.Fatalf("expected empty plan echo, got %s", data)
	}
}

func TestPlanUpdateToolDescriptorIsRegisteredInKernelPalette(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	provider := newKernelToolProvider(toolCatalogBuilder, toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, bluecollar.NewToolSet(nil))

	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name != bluecollar.PlanUpdateToolName {
			continue
		}
		if boundTool.Definition.SideEffectClass != bluecollar.ToolSideEffectNone {
			t.Fatalf("expected a side-effect-free plan tool, got %+v", boundTool.Definition)
		}
		if boundTool.Definition.Completion.Mode != bluecollar.ToolCompletionNone {
			t.Fatalf("expected completion mode none, got %+v", boundTool.Definition.Completion)
		}
		return
	}
	t.Fatal("expected plan.update in the kernel palette")
}
