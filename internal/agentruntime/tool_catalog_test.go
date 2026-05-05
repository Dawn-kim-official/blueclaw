package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/config"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func TestFileAttachToolAttachesMultiplePaths(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(workspacePath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(workspacePath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(workspacePath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"paths": []string{"deck.pptx", "deck.pdf", "deck.html", "deck-notes.txt"},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected successful attachment result, got %s", result.Content)
	}
	if len(result.Attachments) != 4 {
		t.Fatalf("expected four attachments, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" || result.Attachments[3].Filename != "deck-notes.txt" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
}

func TestFileToolsAcceptAgentWorkspacePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	writeResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/deck/presentation.md",
			"content": "# Deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.IsError {
		t.Fatalf("expected file.write success, got %s", writeResult.Content)
	}
	if strings.Contains(writeResult.Content, workspacePath) {
		t.Fatalf("expected file.write result not to expose host path, got %s", writeResult.Content)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.IsError {
		t.Fatalf("expected file.attach success, got %s", attachResult.Content)
	}
	if attachResult.Attachments[0].DevicePath != "/workspace/deck/presentation.md" {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestFileToolsDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !writeResult.IsError || !strings.Contains(writeResult.Content, "cannot write") {
		t.Fatalf("expected file.write denial, got %+v", writeResult)
	}

	attachResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.md",
		}),
	})
	if errorValue == nil {
		t.Fatalf("expected file.attach access error, got %+v", attachResult)
	}
	if !strings.Contains(errorValue.Error(), "cannot read") {
		t.Fatalf("expected file.attach read denial, got %v", errorValue)
	}
}

func TestFileToolsAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected finance member write success, got %+v", result)
	}
}

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "mkdir -p /workspace/deck && printf ok > /workspace/deck/result.txt",
			"workingDirectoryPath": "/workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected terminal.run success, got %s", result.Content)
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "deck", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunDenyCircleWorkingDirectoryForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "printf no",
			"workingDirectoryPath": "/workspace/circles/finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.IsError || !strings.Contains(result.Content, "cannot use this workspace path") {
		t.Fatalf("expected terminal.run path denial, got %+v", result)
	}
}

func TestSkillAddCreatesUserManagedSkillAndRefreshes(t *testing.T) {
	workspacePath := t.TempDir()
	refreshCount := 0
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseSkillChangeHandler(func(context.Context) {
		refreshCount++
	})
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "research-helper",
			"content": userSkillDocument("research-helper"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected skill.add success, got %s", result.Content)
	}
	if refreshCount != 1 {
		t.Fatalf("expected skill index refresh, got %d", refreshCount)
	}
	skillDocumentPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper", "SKILL.md")
	document, errorValue := os.ReadFile(skillDocumentPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(document), "Research helper handles source lookups.") {
		t.Fatalf("expected skill document to be written, got %s", string(document))
	}
	if strings.Contains(result.Content, workspacePath) || !strings.Contains(result.Content, "/workspace/.agents/skills/research-helper/SKILL.md") {
		t.Fatalf("expected agent workspace path in result, got %s", result.Content)
	}
	resultDocument := decodeSkillAddResult(t, result.Content)
	if resultDocument.Name != "research-helper" || resultDocument.Status != "created" {
		t.Fatalf("expected structured skill.add result, got %+v", resultDocument)
	}
}

func TestSkillAddWritesAllowedResources(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: report-helper
description: Help create reports from source material.
when_to_use: Use for report writing requests.
---
Use references/reporting.md and scripts/build_report.sh when needed.
`

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]any{
			"name":    "report-helper",
			"content": content,
			"resources": []map[string]any{
				{"path": "references/reporting.md", "content": "# Reporting"},
				{"path": "scripts/build_report.sh", "content": "echo ok", "mode": 0o700},
				{"path": "assets/template.txt", "content": "template"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected skill.add success, got %s", result.Content)
	}
	for _, path := range []string{"references/reporting.md", "scripts/build_report.sh", "assets/template.txt"} {
		if _, errorValue := os.Stat(filepath.Join(workspacePath, ".agents", "skills", "report-helper", path)); errorValue != nil {
			t.Fatalf("expected resource %s: %v", path, errorValue)
		}
	}
	resultDocument := decodeSkillAddResult(t, result.Content)
	if len(resultDocument.ResourcePaths) != 3 {
		t.Fatalf("expected resource paths in result, got %+v", resultDocument)
	}
}

func TestSkillRemoveDeletesOnlyUserManagedSkill(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(skillDirectoryPath, "SKILL.md"), userSkillDocument("research-helper"))
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "research-helper",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected skill.remove success, got %s", result.Content)
	}
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected user-managed skill directory removed, got %v", errorValue)
	}
}

func TestSkillRemoveMissingSkillIsNonFatal(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "missing-skill",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError || !strings.Contains(result.Content, `"status":"missing"`) {
		t.Fatalf("expected non-fatal missing result, got %+v", result)
	}
}

func TestSkillManagementRejectsInvalidAndBuiltInNames(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	for _, input := range []map[string]string{
		{"name": "../escape", "content": userSkillDocument("escape")},
		{"name": "simple-slides", "content": userSkillDocument("simple-slides")},
	} {
		result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input:    agent.MarshalToolInput(input),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.IsError {
			t.Fatalf("expected skill.add to reject %+v", input)
		}
	}

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "agent-browser",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.IsError {
		t.Fatalf("expected skill.remove to reject built-in skill, got %+v", result)
	}
}

func TestSkillAddRejectsMalformedOrCustomFrontmatter(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	for _, content := range []string{
		"---\nname: broken\ndescription: Broken",
		"---\nname: custom\nsummary: no\n---\nBody.",
		"---\nname: custom\ntags: [one]\n---\nBody.",
		"---\nname: custom\ntriggerHints: [one]\n---\nBody.",
		"---\nname: custom\nrequiredTools: [terminal.run]\n---\nBody.",
		"---\nname: custom\nallowedProfiles: [default]\n---\nBody.",
	} {
		result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input: agent.MarshalToolInput(map[string]string{
				"name":    "broken",
				"content": content,
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.IsError {
			t.Fatalf("expected malformed skill document to be rejected: %s", content)
		}
	}
}

func TestSkillAddRejectsInvalidResourcePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	for _, resourcePath := range []string{
		"../escape.md",
		"/workspace/escape.md",
		"SKILL.md",
		".hidden/file.md",
		"notes/file.md",
	} {
		result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input: agent.MarshalToolInput(map[string]any{
				"name":    "resource-helper",
				"content": userSkillDocument("resource-helper"),
				"resources": []map[string]string{{
					"path":    resourcePath,
					"content": "no",
				}},
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.IsError {
			t.Fatalf("expected resource path %q to be rejected", resourcePath)
		}
	}
}

func TestSkillAddReturnsQualityWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: tiny-helper
description: Tiny.
---
Use references/missing.md.
`

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]any{
			"name":    "tiny-helper",
			"content": content,
			"resources": []map[string]string{{
				"path":    "assets/unmentioned.txt",
				"content": "asset",
			}},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected warning-only skill.add success, got %s", result.Content)
	}
	resultDocument := decodeSkillAddResult(t, result.Content)
	for _, expectedWarning := range []string{
		"when_to_use is recommended so retrieval has explicit trigger context",
		"description is short; include what the skill does and when to use it",
		"SKILL.md mentions references/ but no reference resources were supplied",
		"resource assets/unmentioned.txt is not mentioned from SKILL.md",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestSkillAddReturnsLongBodyAndMissingScriptWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: long-helper
description: Help with long deterministic workflows.
when_to_use: Use for long workflow requests.
---
Use scripts/missing.sh when needed.
` + strings.Repeat("step\n", longSkillBodyLineCount+1)

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "long-helper",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected warning-only skill.add success, got %s", result.Content)
	}
	resultDocument := decodeSkillAddResult(t, result.Content)
	for _, expectedWarning := range []string{
		"skill body is long; move detailed material into references",
		"SKILL.md mentions scripts/ but no script resources were supplied",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestFileWriteRejectsBuiltInSkillPaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
	for _, path := range []string{
		"/workspace/skills/bash/SKILL.md",
		"/workspace/skills/.internkim-skills-manifest.json",
		"/workspace/.agents/skills/agent-browser/SKILL.md",
	} {
		result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
			ToolName: "file.write",
			Input: agent.MarshalToolInput(map[string]string{
				"path":    path,
				"content": "no",
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.IsError {
			t.Fatalf("expected file.write to reject immutable skill path %q", path)
		}
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func userSkillDocument(skillName string) string {
	return `---
name: ` + skillName + `
when_to_use: Use for research and source lookup requests.
allowed-tools:
  - memory.search
---
Research helper handles source lookups.
`
}

func decodeSkillAddResult(t *testing.T, content string) skillAddResult {
	t.Helper()
	var resultDocument skillAddResult
	if errorValue := json.Unmarshal([]byte(content), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	return resultDocument
}

func containsTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
