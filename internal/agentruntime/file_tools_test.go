package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/policy"
)

func TestFileAttachToolAttachesSinglePath(t *testing.T) {
	workspacePath := t.TempDir()
	requesterDeckPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"path": "tmp/deck/deck.pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
}

func TestFileAttachToolAttachesMultipleFiles(t *testing.T) {
	workspacePath := t.TempDir()
	requesterDeckPath := filepath.Join(workspacePath, "private", "people", "person-1", "artifacts", "deck")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pptx"), "pptx")

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"files": []map[string]string{
				{"path": "artifacts/deck/deck.html", "contentType": "text/html"},
				{"path": "artifacts/deck/deck.pptx", "contentType": "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.html" || result.Attachments[1].Filename != "deck.pptx" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
}

func TestFileToolsAcceptVirtualHomePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "home/projects/deck/presentation.md",
			"content": "# Deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	if strings.Contains(writeResult.ContentText(), workspacePath) {
		t.Fatalf("expected file.write result not to expose host path, got %s", writeResult.ContentText())
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "home/projects/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.Failed() {
		t.Fatalf("expected file.attach success, got %s", attachResult.ContentText())
	}
	expectedDevicePath := "/workspace/private/people/person-1/projects/deck/presentation.md"
	if attachResult.Attachments[0].DevicePath != expectedDevicePath {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestFileWriteAcceptsPortablePathAndContent(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "home/projects/site/index.html",
			"content": "<html>ready</html>",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	document, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "site", "index.html"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "<html>ready</html>" {
		t.Fatalf("expected content to be written, got %q", string(document))
	}
}

func TestFileWriteDescribesContentAsExactFileBody(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolDefinition, isFound := toolRegistry.ToolDefinition("file.write")
	if !isFound {
		t.Fatal("expected file.write definition")
	}
	if !strings.Contains(toolDefinition.Description, "complete file body") {
		t.Fatalf("expected file.write description to explain exact file body, got %q", toolDefinition.Description)
	}
	if !strings.Contains(string(toolDefinition.InputSchema), "real line breaks") {
		t.Fatalf("expected file.write schema to explain multiline content, got %s", string(toolDefinition.InputSchema))
	}
	if !strings.Contains(toolDefinition.RecoveryCard.AvoidWhen, "escaped newline sequences") {
		t.Fatalf("expected file.write recovery card to warn about escaped newlines, got %+v", toolDefinition.RecoveryCard)
	}
}

func TestFileReadResultByteWindowPaginates(t *testing.T) {
	content := "abcdefghij"
	result := fileReadResult(content, fileReadToolInput{StartByte: 3}, 4)
	if result.Content != "defg" {
		t.Fatalf("content = %q", result.Content)
	}
	if result.StartByte != 3 || result.EndByte != 7 || result.NextByte != 7 || result.TotalBytes != 10 {
		t.Fatalf("byte metadata = %+v", result)
	}
	if result.IsEndOfFile {
		t.Fatal("expected more content after window")
	}
	final := fileReadResult(content, fileReadToolInput{StartByte: result.NextByte}, 100)
	if final.Content != "hij" || final.NextByte != 0 || !final.IsEndOfFile {
		t.Fatalf("final window = %+v", final)
	}
}

func TestFileReadResultByteWindowSnapsRuneBoundary(t *testing.T) {
	content := "a가나다"
	result := fileReadResult(content, fileReadToolInput{StartByte: 2}, 4)
	if !utf8.ValidString(result.Content) {
		t.Fatalf("expected valid UTF-8 window, got %q", result.Content)
	}
}

func TestFileReadResultLineOverrunReturnsByteHint(t *testing.T) {
	content := "line1\nline2\n" + strings.Repeat("x", 5000)
	result := fileReadResult(content, fileReadToolInput{StartLine: 9}, 200)
	if result.Content != "" {
		t.Fatalf("expected empty content past last line, got %q", result.Content)
	}
	if !result.IsEndOfFile || !strings.Contains(result.ReadHint, "startByte") {
		t.Fatalf("expected end-of-file byte hint, got %+v", result)
	}
}

func TestFileReadReturnsLineRangeMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "one\ntwo\nthree\nfour\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]any{
			"path":      "tmp/source.ts",
			"startLine": 2,
			"lineCount": 2,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected file.read success, got %s", readResult.ContentText())
	}
	resultData := map[string]any{}
	if errorValue := json.Unmarshal([]byte(readResult.ContentText()), &resultData); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultData["content"] != "two\nthree" || resultData["startLine"] != float64(2) || resultData["endLine"] != float64(3) || resultData["totalLines"] != float64(4) {
		t.Fatalf("expected line range metadata, got %+v", resultData)
	}
	if resultData["originalSizeBytes"] == nil || resultData["returnedBytes"] == nil || resultData["totalLinesKnown"] != true {
		t.Fatalf("expected honest size metadata, got %+v", resultData)
	}
}

func TestFileReadReturnsRangeAfterOldPrefixLimit(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "large-source.txt")
	lines := []string{}
	for index := 0; index < 3000; index++ {
		lines = append(lines, strings.Repeat("x", 80))
	}
	lines[2500] = "target-after-prefix"
	writeTestFile(t, filePath, strings.Join(lines, "\n"))
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]any{
			"path":      "tmp/large-source.txt",
			"startLine": 2501,
			"lineCount": 1,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected file.read success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), "target-after-prefix") {
		t.Fatalf("expected line beyond former prefix limit, got %s", readResult.ContentText())
	}
}

func TestFilePreviewReturnsTextPreview(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.html")
	writeTestFile(t, filePath, "<h1>Preview Title</h1>\n<p>Preview body</p>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"path": "tmp/source.html"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected file.preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Preview Title") || !strings.Contains(previewResult.ContentText(), `"conversionStatus":"converted"`) {
		t.Fatalf("expected text preview, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewUsesCachedAttachmentPreview(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.pdf")
	writeTestFile(t, filePath, "%PDF")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agent.AgentPart{{
			Type: agent.AgentPartTypeFile,
			File: &agent.AgentFilePart{
				Path:             "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf",
				Filename:         "report.pdf",
				ContentType:      "application/pdf",
				SizeBytes:        4,
				MarkdownPreview:  "# Cached report",
				ConversionStatus: "converted",
			},
		}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"path": "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected cached file.preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "# Cached report") {
		t.Fatalf("expected cached preview, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewUsesCachedAttachmentPreviewByMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agent.AgentPart{{
			Type: agent.AgentPartTypeFile,
			File: &agent.AgentFilePart{
				Path:             "home/inbox/mattermost/post-1/report.html",
				Filename:         "report.html",
				ContentType:      "text/html",
				SizeBytes:        42,
				MarkdownPreview:  "# Cached material report",
				ConversionStatus: "converted",
			},
			Source: agent.AgentPartSource{
				Platform:  "mattermost",
				MessageID: "post-1",
				FileID:    "file-1",
			},
		}},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected cached material preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "# Cached material report") {
		t.Fatalf("expected cached material preview, got %s", previewResult.ContentText())
	}
}

func TestFileReadUsesCachedAttachmentPreviewWhenMaterialFileIsNotMounted(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		InputParts: []agent.AgentPart{{
			Type: agent.AgentPartTypeFile,
			File: &agent.AgentFilePart{
				Path:             "home/inbox/mattermost/post-1/report.html",
				Filename:         "report.html",
				ContentType:      "text/html",
				SizeBytes:        42,
				MarkdownPreview:  "# Cached read report\n\nBody",
				ConversionStatus: "converted",
			},
			Source: agent.AgentPartSource{
				Platform:  "mattermost",
				MessageID: "post-1",
				FileID:    "file-1",
			},
		}},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]any{
			"path":      "home/inbox/mattermost/post-1/report.html",
			"startLine": 3,
			"lineCount": 1,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected cached file.read success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), `"source":"attachmentPreview"`) || !strings.Contains(readResult.ContentText(), "Body") {
		t.Fatalf("expected cached attachment read, got %s", readResult.ContentText())
	}
}

func TestFilePreviewResolvesAttachmentMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.html")
	writeTestFile(t, filePath, "<h1>Material Preview</h1>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "home/inbox/mattermost/post-1/report.html",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected material file.preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Material Preview") {
		t.Fatalf("expected material preview content, got %s", previewResult.ContentText())
	}
}

func TestFilePreviewFallsBackFromStaleAttachmentPathToMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "report.html")
	writeTestFile(t, filePath, "<h1>Recovered Preview</h1>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agent.VisibleContext{
			CurrentMaterials: []agent.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "home/inbox/mattermost/old/report.html",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "home/inbox/mattermost/post-1/report.html",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"path": "home/inbox/mattermost/thread-1/post-1/report.html"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected stale path fallback success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Recovered Preview") {
		t.Fatalf("expected recovered preview content, got %s", previewResult.ContentText())
	}
}

func TestFileReadFallsBackFromStaleAttachmentPathToMaterialID(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "inbox", "mattermost", "post-1", "kim-intern-automation.html")
	writeTestFile(t, filePath, "<h1>Recovered Read</h1>\n<p>Body</p>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agent.VisibleContext{
			CurrentMaterials: []agent.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "kim-intern-automation.html",
				ContentType: "text/html",
				Path:        "home/inbox/mattermost/old/kim-intern-automation.html",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "kim-intern-automation.html",
				ContentType: "text/html",
				Path:        "home/inbox/mattermost/post-1/kim-intern-automation.html",
			},
		},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input:    agent.MarshalToolInput(map[string]any{"path": "home/inbox/mattermost/thread-1/post-1/kim-intern-automation.html"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() {
		t.Fatalf("expected stale path file.read fallback success, got %s", readResult.ContentText())
	}
	if !strings.Contains(readResult.ContentText(), "Recovered Read") || strings.Contains(readResult.ContentText(), `"source":"attachmentPreview"`) {
		t.Fatalf("expected exact recovered file read, got %s", readResult.ContentText())
	}
}

func TestFileReadRejectsImageAttachmentMaterialFallback(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		VisibleContext: agent.VisibleContext{
			CurrentMaterials: []agent.VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "home/inbox/mattermost/old/mascot.png",
			}},
		},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:  "mattermost:file-1",
				Filename:    "mascot.png",
				ContentType: "image/png",
				Path:        "home/inbox/mattermost/post-1/mascot.png",
			},
		},
	})

	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input:    agent.MarshalToolInput(map[string]any{"path": "home/inbox/mattermost/thread-1/post-1/mascot.png"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !readResult.Failed() || !strings.Contains(readResult.ContentText(), "use image.read") {
		t.Fatalf("expected image attachment file.read fallback to point at image.read, got %s", readResult.ContentText())
	}
}

func TestFilePreviewUsesResolvedAttachmentPreviewWithoutWorkspaceStat(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		AttachmentMaterialResolver: staticAttachmentMaterialResolver{
			material: agent.VisibleContextMaterial{
				MaterialID:        "mattermost:file-1",
				Filename:          "report.html",
				ContentType:       "text/html",
				Path:              "home/inbox/mattermost/post-1/report.html",
				MarkdownPreview:   "<h1>Resolved Preview</h1>",
				ConversionStatus:  "converted",
				ConversionMessage: "raw text preview",
			},
		},
	})

	previewResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.preview",
		Input:    agent.MarshalToolInput(map[string]any{"materialID": "mattermost:file-1"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if previewResult.Failed() {
		t.Fatalf("expected resolved attachment preview success, got %s", previewResult.ContentText())
	}
	if !strings.Contains(previewResult.ContentText(), "Resolved Preview") {
		t.Fatalf("expected resolved preview content, got %s", previewResult.ContentText())
	}
}

func TestFileEditReplacesSingleExactMatch(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "const title = 'Old';\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	editResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.edit",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "tmp/source.ts",
			"oldText": "Old",
			"newText": "New",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if editResult.Failed() {
		t.Fatalf("expected file.edit success, got %s", editResult.ContentText())
	}
	document, errorValue := os.ReadFile(filePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "const title = 'New';\n" {
		t.Fatalf("expected exact replacement, got %q", string(document))
	}
}

func TestFileEditRejectsAmbiguousExactMatch(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "source.ts")
	writeTestFile(t, filePath, "same\nsame\n")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	editResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.edit",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "tmp/source.ts",
			"oldText": "same",
			"newText": "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !editResult.Failed() {
		t.Fatalf("expected ambiguous file.edit to fail, got %s", editResult.ContentText())
	}
	if editResult.Failure.Stage != "file_edit" || !strings.Contains(editResult.ContentText(), `"matchCount":2`) {
		t.Fatalf("expected match count failure, got %+v %s", editResult.Failure, editResult.ContentText())
	}
	document, errorValue := os.ReadFile(filePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "same\nsame\n" {
		t.Fatalf("expected failed edit not to modify file, got %q", string(document))
	}
}

func TestFilePatchAppliesMultipleExactEdits(t *testing.T) {
	workspacePath := t.TempDir()
	firstPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "one.ts")
	secondPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "two.ts")
	writeTestFile(t, firstPath, "alpha")
	writeTestFile(t, secondPath, "beta")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	patchResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.patch",
		Input: agent.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/one.ts", "oldText": "alpha", "newText": "ALPHA"},
				{"path": "tmp/two.ts", "oldText": "beta", "newText": "BETA"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if patchResult.Failed() {
		t.Fatalf("expected file.patch success, got %s", patchResult.ContentText())
	}
	assertTestFileContent(t, firstPath, "ALPHA")
	assertTestFileContent(t, secondPath, "BETA")
}

func TestFilePatchValidationIsAllOrNothing(t *testing.T) {
	workspacePath := t.TempDir()
	firstPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "one.ts")
	secondPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "two.ts")
	writeTestFile(t, firstPath, "alpha")
	writeTestFile(t, secondPath, "beta")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	patchResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.patch",
		Input: agent.MarshalToolInput(map[string]any{
			"edits": []map[string]string{
				{"path": "tmp/one.ts", "oldText": "alpha", "newText": "ALPHA"},
				{"path": "tmp/two.ts", "oldText": "missing", "newText": "BETA"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !patchResult.Failed() {
		t.Fatalf("expected file.patch failure, got %s", patchResult.ContentText())
	}
	if !strings.Contains(patchResult.ContentText(), `"editIndex":1`) || !strings.Contains(patchResult.ContentText(), `"matchCount":0`) {
		t.Fatalf("expected failing edit metadata, got %s", patchResult.ContentText())
	}
	assertTestFileContent(t, firstPath, "alpha")
	assertTestFileContent(t, secondPath, "beta")
}

func TestFileToolsDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !writeResult.Failed() || !strings.Contains(writeResult.ContentText(), "cannot write") {
		t.Fatalf("expected file.write denial, got %+v", writeResult)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !attachResult.Failed() || !strings.Contains(attachResult.ContentText(), "cannot read") {
		t.Fatalf("expected file.attach read denial, got %+v", attachResult)
	}
}

func TestFileReadCapabilityDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.pdf"), "secret")
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.pdf",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "cannot read") {
		t.Fatalf("expected read denial, got %+v", result)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied file.read not to call capability bridge, got path=%s", httpClient.requestPath)
	}
}

func TestFileReadCapabilityAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	httpClient := &recordingHTTPClient{responseBody: `{"content":"# Report","status":"ok","result":{"content":"# Report"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), `"content":"secret"`) {
		t.Fatalf("expected file.read success, got %+v", result)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected built-in file.read not to call capability bridge, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

func TestFileToolsAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected finance member write success, got %+v", result)
	}
}

func TestFileWriteDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
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
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "notes.md",
			"content": "private",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "notes.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "private" {
		t.Fatalf("expected private file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(result.ContentText(), `"tmp/notes.md"`) {
		t.Fatalf("expected private agent path in result, got %s", result.ContentText())
	}
}

func TestFileWriteDefaultsToCircleScopeForCircleChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "channel:channel-1",
		ConversationType:        "P",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "circle-finance",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected circle write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "report.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "finance" {
		t.Fatalf("expected finance file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileWriteDefaultsToStaffScopeForGeneralChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "thread:channel-1:post-1",
		ConversationType:        "O",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "town-square",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "status.md",
			"content": "staff",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected staff write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "status.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "staff" {
		t.Fatalf("expected staff file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileAttachDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	privateDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp")
	if errorValue := os.MkdirAll(privateDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(privateDirectoryPath, "notes.md"), "private")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
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
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "notes.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private attach success, got %+v", result)
	}
	if result.Attachments[0].DevicePath != "/workspace/private/people/person-1/tmp/notes.md" {
		t.Fatalf("expected private device path, got %+v", result.Attachments[0])
	}
}

func TestFilePromoteCopiesDraftOutputToArtifacts(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
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
			"path":    "tmp/deck/build/deck.pptx",
			"content": "pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	promoteResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.promote",
		Input: agent.MarshalToolInput(map[string]any{
			"path":                     "tmp/deck/build/deck.pptx",
			"destinationDirectoryPath": "artifacts/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if promoteResult.Failed() {
		t.Fatalf("expected file.promote success, got %s", promoteResult.ContentText())
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "artifacts", "deck", "deck.pptx")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "pptx" {
		t.Fatalf("expected promoted file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(promoteResult.ContentText(), `"artifacts/deck/deck.pptx"`) {
		t.Fatalf("expected promoted virtual path, got %s", promoteResult.ContentText())
	}
}

func TestFileWriteProtectsManagedSitePackageManifest(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"file.write"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	managedResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "home/sites/site-1/draft/app/package.json",
			"content": `{"project":"not a package manifest"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !managedResult.Failed() || managedResult.Failure.Code != "managed_manifest_protected" {
		t.Fatalf("expected managed manifest protection, got %+v", managedResult)
	}

	tmpResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/demo/package.json",
			"content": `{"project":"freeform package file"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if tmpResult.Failed() {
		t.Fatalf("expected tmp package manifest write to remain allowed, got %+v", tmpResult)
	}
}

func TestFileWriteThroughWorkspaceActorTreatsContentAsData(t *testing.T) {
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

	content := "hello\n$(touch should-not-exist)\n"
	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "tmp/deck/input.txt",
			"content": content,
			"mode":    0600,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	taskTmpPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp")
	document, errorValue := os.ReadFile(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != content {
		t.Fatalf("expected exact content, got %q", string(document))
	}
	if _, errorValue := os.Stat(filepath.Join(taskTmpPath, "should-not-exist")); !os.IsNotExist(errorValue) {
		t.Fatalf("file.write content must not be executed as shell, stat error %v", errorValue)
	}
	fileInformation, errorValue := os.Stat(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0600 {
		t.Fatalf("expected requester chmod mode 0600, got %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteUsesPrivateModesForTerminalFlow(t *testing.T) {
	workspacePath := t.TempDir()
	previousMask := syscall.Umask(0027)
	defer syscall.Umask(previousMask)

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
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

	deckDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	directoryInformation, errorValue := os.Stat(deckDirectoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if directoryInformation.Mode().Perm() != 0700 {
		t.Fatalf("expected private deck directory mode 0700, got mode %v", directoryInformation.Mode().Perm())
	}
	fileInformation, errorValue := os.Stat(filepath.Join(deckDirectoryPath, "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0600 {
		t.Fatalf("expected private file mode 0600 for requester terminal flow, got mode %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteAndTerminalRunShareRequesterWorkspaceActorView(t *testing.T) {
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
			"content": "same workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	runResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": "tmp/deck",
			"command":              "mkdir -p build && cat input.txt > build/output.txt",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runResult.Failed() {
		t.Fatalf("expected terminal.run success, got %s", runResult.ContentText())
	}
	outputPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "output.txt")
	document, errorValue := os.ReadFile(outputPath)
	if errorValue != nil || string(document) != "same workspace" {
		t.Fatalf("expected terminal to read file.write output, got %q and %v", string(document), errorValue)
	}
}

func TestFileWriteWithoutRequesterIdentityDoesNotFallbackToServiceUser(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "no service fallback",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Stage != "actor_identity_missing" {
		t.Fatalf("expected actor identity failure, got %+v", result)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck", "input.txt")); !os.IsNotExist(errorValue) {
		t.Fatalf("file.write must not fall back to service-user workspace writes, stat error %v", errorValue)
	}
}

func TestFileWriteRejectsBuiltInSkillPaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, path := range []string{
		"/workspace/skills/bash/SKILL.md",
		"/workspace/skills/.internkim-skills-manifest.json",
		"/workspace/.agents/skills/agent-browser/SKILL.md",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
			ToolName: "file.write",
			Input: agent.MarshalToolInput(map[string]string{
				"path":    path,
				"content": "no",
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected file.write to reject immutable skill path %q", path)
		}
	}
}


func TestApplyExactOrTolerantEditExactMatch(t *testing.T) {
	updated, count, applied := applyExactOrTolerantEdit("alpha\nbeta\ngamma\n", "beta", "BETA")
	if !applied || count != 1 || updated != "alpha\nBETA\ngamma\n" {
		t.Fatalf("expected exact replace, got applied=%v count=%d %q", applied, count, updated)
	}
}

func TestApplyExactOrTolerantEditRejectsAmbiguous(t *testing.T) {
	_, count, applied := applyExactOrTolerantEdit("same\nsame\n", "same", "changed")
	if applied || count != 2 {
		t.Fatalf("expected ambiguous rejection with count 2, got applied=%v count=%d", applied, count)
	}
}

func TestApplyExactOrTolerantEditToleratesLeadingWhitespace(t *testing.T) {
	content := "func main() {\n        callDeep()\n}\n"
	// model wrote the block with 4-space indent; file has 8 spaces
	updated, _, applied := applyExactOrTolerantEdit(content, "    callDeep()", "    callShallow()")
	if !applied {
		t.Fatal("expected leading-whitespace-tolerant match to apply")
	}
	if updated != "func main() {\n        callShallow()\n}\n" {
		t.Fatalf("expected replacement re-indented to file's 8 spaces, got %q", updated)
	}
}

func TestApplyExactOrTolerantEditToleratesTrailingWhitespace(t *testing.T) {
	// File lines carry trailing whitespace the model omitted, so the exact
	// multi-line substring does not match and the tolerant tier must apply.
	content := "alpha  \nbeta\n"
	updated, _, applied := applyExactOrTolerantEdit(content, "alpha\nbeta", "ALPHA\nBETA")
	if !applied || updated != "ALPHA\nBETA\n" {
		t.Fatalf("expected trailing-whitespace-tolerant match, got applied=%v %q", applied, updated)
	}
}

func TestApplyExactOrTolerantEditNormalizesSmartQuotes(t *testing.T) {
	content := "const greeting = ‘hello’;\n"
	updated, _, applied := applyExactOrTolerantEdit(content, "const greeting = 'hello';", "const greeting = 'hi';")
	if !applied || updated != "const greeting = 'hi';\n" {
		t.Fatalf("expected smart-quote normalized match, got applied=%v %q", applied, updated)
	}
}

func TestApplyExactOrTolerantEditFailsWhenAbsent(t *testing.T) {
	_, count, applied := applyExactOrTolerantEdit("alpha\nbeta\n", "totally-different-line", "x")
	if applied || count != 0 {
		t.Fatalf("expected absent oldText to fail, got applied=%v count=%d", applied, count)
	}
}

func TestFileEditMatchFailureGuidanceSuggestsClosestLines(t *testing.T) {
	guidance := fileEditMatchFailureGuidance("export const Button = () => null;\n", "export const Buttons = () => null;", 0)
	if !strings.Contains(guidance, "closest existing lines") || !strings.Contains(guidance, "export const Button") {
		t.Fatalf("expected closest-line suggestion, got %q", guidance)
	}
}
