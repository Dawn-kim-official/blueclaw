package agentruntime

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"blueclaw/internal/agent"
)

func TestMathCalculateToolEvaluatesArithmeticWithBC(t *testing.T) {
	if _, errorValue := exec.LookPath("bc"); errorValue != nil {
		t.Skip("bc is not installed")
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"math.calculate"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "math.calculate",
		Input:    agent.MarshalToolInput(map[string]string{"expression": "(2+3)*4"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected calculation success, got %s", result.ContentText())
	}
	var document map[string]string
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if document["result"] != "20" {
		t.Fatalf("expected result 20, got %+v", document)
	}
}

func TestMathCalculateToolTranslatesDoubleAsterisk(t *testing.T) {
	if _, errorValue := exec.LookPath("bc"); errorValue != nil {
		t.Skip("bc is not installed")
	}
	result, errorValue := runBCExpression(context.Background(), mustNormalizeMathExpression(t, "2**10"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result != "1024" {
		t.Fatalf("expected 1024, got %q", result)
	}
}

func TestMathCalculateToolRejectsUnsafeExpressions(t *testing.T) {
	for _, expression := range []string{`open("x")`, `__import__("os")`, `a=1`, `sqrt(2)`} {
		if _, errorValue := normalizeMathExpression(expression); errorValue == nil {
			t.Fatalf("expected expression to be rejected: %s", expression)
		}
	}
}

func mustNormalizeMathExpression(t *testing.T, expression string) string {
	t.Helper()
	normalizedExpression, errorValue := normalizeMathExpression(expression)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return normalizedExpression
}
