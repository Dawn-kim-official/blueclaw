package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunCommandRequestTreatsCommandWithArgumentsAsExecutable(t *testing.T) {
	input := terminalRunToolInput{
		Command: "/workspace/tools/capability",
		Arguments: []string{
			"/workspace/tools/capability",
			"invoke",
			"flow.task.add",
			`{"prompt":"test"}`,
		},
		WorkingDirectoryPath: "/workspace",
	}

	commandRequest := input.commandRequest()
	if commandRequest.Command != "" {
		t.Fatalf("expected shell command to be cleared, got %q", commandRequest.Command)
	}
	if commandRequest.ExecutableName != "/workspace/tools/capability" {
		t.Fatalf("expected executable command, got %q", commandRequest.ExecutableName)
	}
	expectedArguments := []string{"invoke", "flow.task.add", `{"prompt":"test"}`}
	if strings.Join(commandRequest.Arguments, "\n") != strings.Join(expectedArguments, "\n") {
		t.Fatalf("expected normalized arguments %+v, got %+v", expectedArguments, commandRequest.Arguments)
	}
}

func TestTerminalRunRejectsSourceWriteHeredoc(t *testing.T) {
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
			"command":              "cat <<'EOF' > app/src/index.css\nbody { color: black; }\nEOF",
			"workingDirectoryPath": "tmp/site",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected terminal.run source write failure, got %s", result.ContentText())
	}
	if result.Failure.Stage != "terminal_source_write" {
		t.Fatalf("expected terminal_source_write stage, got %+v", result.Failure)
	}
	if !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "terminal.run") {
		t.Fatalf("expected terminal.run recovery hint, got %+v", result.Failure.RecoveryHints)
	}
}

func TestTerminalRunRejectsSourceFileRedirection(t *testing.T) {
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
			"command":              "printf 'export default function App(){}' > app/src/App.tsx",
			"workingDirectoryPath": "tmp/site",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected terminal.run source write failure, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "bundled skill scripts") {
		t.Fatalf("expected source write failure to suggest bundled skill scripts, got %s", result.ContentText())
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

func TestTerminalRunPathGuardrailFailureIsRecoverable(t *testing.T) {
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
			"command": "/opt/blueclaw/builtin-skills-venv/bin/python --version",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected terminal.run path guardrail failure, got %+v", result)
	}
	if result.Failure.Code != agent.FailureCodes.InvalidInput.String() || result.Failure.Stage != "terminal_path_guardrail" {
		t.Fatalf("expected recoverable invalid input failure, got %+v", result.Failure)
	}
	if !result.Failure.Retryable || !result.Failure.SafeRetry {
		t.Fatalf("expected path guardrail failure to be retryable, got %+v", result.Failure)
	}
	for _, expectedText := range []string{
		"/opt/blueclaw/builtin-skills-venv/bin/python",
		"/workspace/skills/<skill>/scripts/skill_runtime.py",
	} {
		if !strings.Contains(result.ContentText(), expectedText) {
			t.Fatalf("expected result to contain %q, got %q", expectedText, result.ContentText())
		}
	}
}

func TestTerminalRunPreflightsNodePackageBuildManifest(t *testing.T) {
	workspacePath := t.TempDir()
	invalidPackagePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "node-app", "package.json")
	writeTestFile(t, invalidPackagePath, `{"project":"not a package manifest"}`)
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": "tmp/node-app",
			"command":              "bun run build",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Code != "package_manifest_invalid" || result.Failure.Stage != "package_manifest_preflight" {
		t.Fatalf("expected package manifest preflight failure, got %+v", result)
	}
	if !strings.Contains(result.ContentText(), "scripts") || !strings.Contains(result.ContentText(), "tmp/node-app/package.json") {
		t.Fatalf("expected manifest detail in result, got %s", result.ContentText())
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
