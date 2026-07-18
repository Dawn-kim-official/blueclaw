package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "mkdir -p build && printf ok > build/result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var resultDocument terminalCommandResultDocument
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != terminalRunModeCommand || !resultDocument.Completed || resultDocument.ExitCode != 0 || resultDocument.TimedOut {
		t.Fatalf("expected canonical completed command result, got %+v", resultDocument)
	}
	if len(result.Effects) != 0 {
		t.Fatalf("expected terminal.run to avoid inferred resource effects, got %+v", result.Effects)
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunCommandRequestPreservesExplicitExecutable(t *testing.T) {
	input := terminalRunToolInput{
		ExecutableName:       "/workspace/tools/capability",
		Arguments:            []string{"invoke", "task.add", `{"prompt":"test"}`},
		WorkingDirectoryPath: "/workspace",
	}

	commandRequest := input.commandRequest()
	if commandRequest.Command != "" {
		t.Fatalf("expected shell command to be cleared, got %q", commandRequest.Command)
	}
	if commandRequest.ExecutableName != "/workspace/tools/capability" {
		t.Fatalf("expected executable command, got %q", commandRequest.ExecutableName)
	}
	expectedArguments := []string{"invoke", "task.add", `{"prompt":"test"}`}
	if strings.Join(commandRequest.Arguments, "\n") != strings.Join(expectedArguments, "\n") {
		t.Fatalf("expected explicit arguments %+v, got %+v", expectedArguments, commandRequest.Arguments)
	}
}

func TestTerminalRunRejectsInvalidInputShapes(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})
	testCases := []struct {
		name  string
		input json.RawMessage
	}{
		{name: "unknown field", input: json.RawMessage(`{"command":"true","unknown":true}`)},
		{name: "fractional timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":1.5}`)},
		{name: "zero timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":0}`)},
		{name: "negative timeout", input: json.RawMessage(`{"command":"true","timeoutSecond":-1}`)},
		{name: "missing command", input: json.RawMessage(`{}`)},
		{name: "ambiguous execution", input: json.RawMessage(`{"command":"true","executableName":"true"}`)},
		{name: "shell arguments", input: json.RawMessage(`{"command":"true","arguments":["one"]}`)},
		{name: "arguments without executable", input: json.RawMessage(`{"arguments":["one"]}`)},
		{name: "session start executable", input: json.RawMessage(`{"mode":"session_start","command":"sh","executableName":"sh"}`)},
		{name: "session write missing input", input: json.RawMessage(`{"mode":"session_write","sessionID":"session-1"}`)},
		{name: "session status command", input: json.RawMessage(`{"mode":"session_status","sessionID":"session-1","command":"true"}`)},
		{name: "session status approval flag", input: json.RawMessage(`{"mode":"session_status","sessionID":"session-1","approvalRequired":false}`)},
		{name: "approval without reason", input: json.RawMessage(`{"command":"true","approvalRequired":true}`)},
		{name: "reason without approval", input: json.RawMessage(`{"command":"true","approvalReason":"required"}`)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: agent.TerminalRunToolName,
				Input:    testCase.input,
			})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureCode() != agent.FailureCodes.InvalidInput.String() {
				t.Fatalf("expected invalid input failure, got %+v", result)
			}
		})
	}
}

func TestTerminalRunFailureHasCanonicalData(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.TerminalRunToolName,
		Input:    json.RawMessage(`{"command":"exit 7"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected command failure, got %+v", result)
	}
	var resultDocument terminalCommandResultDocument
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Mode != terminalRunModeCommand || resultDocument.Completed || resultDocument.ExitCode != 7 {
		t.Fatalf("expected canonical failed command result, got %+v", resultDocument)
	}
}

func TestTerminalRunSessionModesUseCanonicalCompletion(t *testing.T) {
	toolRegistry := newTerminalToolTestCatalogBuilder(t.TempDir()).BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	runningStartResult := invokeTerminalRunTestTool(t, toolRegistry, json.RawMessage(`{"mode":"session_start","command":"sh"}`))
	var runningStartDocument terminalSessionResultDocument
	decodeTerminalRunTestData(t, runningStartResult, &runningStartDocument)
	writeInput := agent.MarshalToolInput(map[string]any{
		"mode":      terminalRunModeSessionWrite,
		"sessionID": runningStartDocument.SessionID,
		"input":     "printf 'ready\\n'\n",
	})
	writeResult := invokeTerminalRunTestTool(t, toolRegistry, writeInput)
	var writeDocument terminalSessionResultDocument
	decodeTerminalRunTestData(t, writeResult, &writeDocument)
	if writeDocument.Completed || writeDocument.Mode != terminalRunModeSessionWrite || writeDocument.SessionID != runningStartDocument.SessionID {
		t.Fatalf("expected incomplete session write, got %+v", writeDocument)
	}
	runningCloseInput := agent.MarshalToolInput(map[string]any{"mode": terminalRunModeSessionClose, "sessionID": runningStartDocument.SessionID})
	invokeTerminalRunTestTool(t, toolRegistry, runningCloseInput)

	startResult := invokeTerminalRunTestTool(t, toolRegistry, json.RawMessage(`{"mode":"session_start","command":"exit 0"}`))
	var startDocument terminalSessionResultDocument
	decodeTerminalRunTestData(t, startResult, &startDocument)
	if startDocument.Completed || startDocument.Mode != terminalRunModeSessionStart || startDocument.SessionID == "" {
		t.Fatalf("expected incomplete session start, got %+v", startDocument)
	}

	var statusDocument terminalSessionResultDocument
	for attempt := 0; attempt < 50; attempt++ {
		statusInput := agent.MarshalToolInput(map[string]any{"mode": terminalRunModeSessionStatus, "sessionID": startDocument.SessionID})
		statusResult := invokeTerminalRunTestTool(t, toolRegistry, statusInput)
		decodeTerminalRunTestData(t, statusResult, &statusDocument)
		if statusDocument.Status == "exited" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if statusDocument.Mode != terminalRunModeSessionStatus || !statusDocument.Completed || statusDocument.ExitCode != 0 {
		t.Fatalf("expected completed exited session status, got %+v", statusDocument)
	}

	closeInput := agent.MarshalToolInput(map[string]any{"mode": terminalRunModeSessionClose, "sessionID": startDocument.SessionID})
	closeResult := invokeTerminalRunTestTool(t, toolRegistry, closeInput)
	var closeDocument terminalSessionCloseResultDocument
	decodeTerminalRunTestData(t, closeResult, &closeDocument)
	if closeDocument.Completed || closeDocument.Status != "closed" {
		t.Fatalf("expected incomplete session close, got %+v", closeDocument)
	}
}

func invokeTerminalRunTestTool(t *testing.T, toolRegistry *agent.ToolSet, input json.RawMessage) agent.ToolResult {
	t.Helper()
	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{ToolName: agent.TerminalRunToolName, Input: input})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %+v", result)
	}
	return result
}

func decodeTerminalRunTestData(t *testing.T, result agent.ToolResult, target any) {
	t.Helper()
	if errorValue := json.Unmarshal(result.Output.Data, target); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestTerminalRunAllowsStderrRedirection(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "printf ok 2>&1",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run stderr redirection success, got %s", result.ContentText())
	}
}

func TestTerminalRunAllowsSourceFileWrite(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "printf 'export default function App(){}' > App.tsx",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("terminal.run must allow writing a source file directly, got %s", result.ContentText())
	}
}

func TestTerminalRunAllowsServiceOwnedPathText(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command": "printf '%s' /workspace/.blueclaw/tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected service-owned path text not to be policy-blocked, got %+v", result)
	}
}

func TestTerminalRunDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command": "pwd",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedSuffix := filepath.Join("private", "people", "person-1", "tmp")
	if !strings.HasSuffix(strings.TrimSpace(commandResult.Stdout), expectedSuffix) {
		t.Fatalf("expected terminal cwd under %s, got %q", expectedSuffix, commandResult.Stdout)
	}
}

func TestTerminalRunMaterializesRequesterRuntimeEnvironment(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"environmentVariables": map[string]string{"PATH": "/workspace/private/people/person-1/bin"},
			"command":              `test -d "$TMPDIR" && test -d "$BUN_TMPDIR" && test -d "$BUN_INSTALL" && printf '%s\n%s\n%s\n%s\n%s' "$HOME" "$PATH" "$TMPDIR" "$BUN_TMPDIR" "$BUN_INSTALL"`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	for _, expectedText := range []string{
		requesterRootPath,
		security.CanonicalRuntimePATH,
		filepath.Join(requesterRootPath, "tmp", ".runtime", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "install"),
	} {
		if !strings.Contains(commandResult.Stdout, expectedText) {
			t.Fatalf("expected runtime environment path %s in stdout, got %q", expectedText, commandResult.Stdout)
		}
		if expectedText != requesterRootPath && expectedText != security.CanonicalRuntimePATH {
			if _, errorValue := os.Stat(expectedText); errorValue != nil {
				t.Fatalf("expected runtime environment directory %s: %v", expectedText, errorValue)
			}
		}
	}
}

func TestTerminalRunRelativeWorkingDirectoryUsesConversationDefault(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "ok",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "pwd && cat input.txt && printf built > result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	if !strings.Contains(commandResult.Stdout, expectedDirectoryPath) || !strings.Contains(commandResult.Stdout, "ok") {
		t.Fatalf("expected terminal cwd and file content under private tmp, got %q", commandResult.Stdout)
	}
	resultDocument, errorValue := os.ReadFile(filepath.Join(expectedDirectoryPath, "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(resultDocument) != "built" {
		t.Fatalf("expected terminal output under private tmp, got %q", string(resultDocument))
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck")); !os.IsNotExist(errorValue) {
		t.Fatalf("terminal.run must not create workspace-root tmp for relative workingDirectoryPath")
	}
}

func TestTerminalRunDenyCircleWorkingDirectoryForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "printf no",
			"workingDirectoryPath": "/workspace/circles/finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "cannot use this workspace path") {
		t.Fatalf("expected terminal.run path denial, got %+v", result)
	}
}
