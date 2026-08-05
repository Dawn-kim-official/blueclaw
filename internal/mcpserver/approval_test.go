package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingApprovalGate struct {
	mutex    sync.Mutex
	outcome  ApprovalOutcome
	requests []ApprovalRequest
}

func (gate *recordingApprovalGate) AwaitApproval(_ context.Context, approvalRequest ApprovalRequest) (ApprovalOutcome, error) {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	gate.requests = append(gate.requests, approvalRequest)
	return gate.outcome, nil
}

func (gate *recordingApprovalGate) received() []ApprovalRequest {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	return append([]ApprovalRequest{}, gate.requests...)
}

func approvalToolSet(t *testing.T, executed *[]string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"file_delete", "file_read"})
	toolSet.AllowTestReplacement()
	register := func(name string, requiresApproval bool, approvalScope string) {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:               "test:" + name,
			Name:             name,
			Description:      name,
			Visibility:       toolcontract.ToolVisibilityModel,
			InputSchema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			RequiresApproval: requiresApproval,
			ApprovalScope:    approvalScope,
			SideEffectClass:  toolcontract.ToolSideEffectStateChange,
			ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			*executed = append(*executed, invocation.ToolName)
			return toolcontract.ToolSuccessData("done", json.RawMessage(`{}`)), nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", name, errorValue)
		}
	}
	register("file_delete", true, "workspace_files")
	register("file_read", false, "")
	return toolSet
}

func callThroughCatalog(t *testing.T, requesterToolSet RequesterToolSet, toolName string) *mcp.CallToolResult {
	t.Helper()
	clientSession := connectedCatalogSession(t, requesterToolSet)
	callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      toolName,
		Arguments: map[string]any{"path": "~/notes.md"},
	})
	if errorValue != nil {
		t.Fatalf("expected the call to reach the catalog: %v", errorValue)
	}
	return callResult
}

func TestACatalogToolNeedingApprovalDoesNotRunWithoutAGate(t *testing.T) {
	executed := []string{}
	callResult := callThroughCatalog(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: approvalToolSet(t, &executed)}, "file_delete")

	if len(executed) != 0 {
		t.Fatalf("expected an approval-gated tool to refuse to run with no gate configured, it ran %+v", executed)
	}
	if !callResult.IsError {
		t.Fatalf("expected the agent to be told the call did not run, got %+v", callResult)
	}
}

func TestACatalogToolNeedingApprovalRunsOnlyOnceApproved(t *testing.T) {
	executed := []string{}
	gate := &recordingApprovalGate{outcome: ApprovalOutcome{Decision: ApprovalDecisionApproved}}
	callResult := callThroughCatalog(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: approvalToolSet(t, &executed), ApprovalGate: gate}, "file_delete")

	if len(executed) != 1 || executed[0] != "file_delete" {
		t.Fatalf("expected an approved call to run once, got %+v", executed)
	}
	if callResult.IsError {
		t.Fatalf("expected an approved call to succeed, got %+v", callResult)
	}
	received := gate.received()
	if len(received) != 1 || received[0].RequesterPersonID != "person-1" || received[0].ApprovalScope != "workspace_files" {
		t.Fatalf("expected the gate to be asked about this requester and scope, got %+v", received)
	}
	if !strings.Contains(string(received[0].ToolInput), "notes.md") {
		t.Fatalf("expected the gate to see the exact call it is approving, got %s", received[0].ToolInput)
	}
}

func TestAHeldOrRejectedCallNeverRuns(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		decision ApprovalDecision
	}{
		{name: "held", decision: ApprovalDecisionHeld},
		{name: "rejected", decision: ApprovalDecisionRejected},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			executed := []string{}
			gate := &recordingApprovalGate{outcome: ApprovalOutcome{Decision: testCase.decision, Notice: "요청자 확인이 필요합니다"}}
			callResult := callThroughCatalog(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: approvalToolSet(t, &executed), ApprovalGate: gate}, "file_delete")

			if len(executed) != 0 {
				t.Fatalf("expected a %s call never to run, it ran %+v", testCase.name, executed)
			}
			if !callResult.IsError {
				t.Fatalf("expected the agent to be told the call did not run, got %+v", callResult)
			}
			textContent, isText := callResult.Content[0].(*mcp.TextContent)
			if !isText || !strings.Contains(textContent.Text, "요청자 확인이 필요합니다") {
				t.Fatalf("expected the gate's own wording to reach the agent, got %+v", callResult.Content)
			}
		})
	}
}

func TestAToolThatNeedsNoApprovalNeverReachesTheGate(t *testing.T) {
	executed := []string{}
	gate := &recordingApprovalGate{outcome: ApprovalOutcome{Decision: ApprovalDecisionHeld}}
	callResult := callThroughCatalog(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: approvalToolSet(t, &executed), ApprovalGate: gate}, "file_read")

	if len(executed) != 1 {
		t.Fatalf("expected an ungated tool to run, got %+v", executed)
	}
	if callResult.IsError {
		t.Fatalf("expected an ungated tool to succeed, got %+v", callResult)
	}
	if len(gate.received()) != 0 {
		t.Fatalf("expected the gate not to be consulted for a tool that needs no approval, got %+v", gate.received())
	}
}
