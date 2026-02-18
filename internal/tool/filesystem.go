package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type workspaceTool struct {
	workspaceDirectory string
}

func (base workspaceTool) resolvePath(path string) (string, error) {
	path = strings.TrimPrefix(path, "/workspace/")
	path = strings.TrimPrefix(path, "/workspace")
	absolute := filepath.Clean(filepath.Join(base.workspaceDirectory, path))
	workspaceBoundary := base.workspaceDirectory + string(filepath.Separator)
	if absolute != base.workspaceDirectory && !strings.HasPrefix(absolute, workspaceBoundary) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return absolute, nil
}

type ReadFileTool struct{ workspaceTool }

func NewReadFileTool(workspaceDirectory string) *ReadFileTool {
	return &ReadFileTool{workspaceTool{workspaceDirectory}}
}
func (tool *ReadFileTool) Name() string        { return "read_file" }
func (tool *ReadFileTool) Description() string { return "Read the contents of a file in /workspace." }
func (tool *ReadFileTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "File path relative to /workspace (e.g. 'notes.md' or '/workspace/notes.md')"},
		},
		"required": []string{"path"},
	}
}
func (tool *ReadFileTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	path, _ := arguments["path"].(string)
	absolute, err := tool.resolvePath(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	contents, err := os.ReadFile(absolute)
	if err != nil {
		return Result{Error: fmt.Sprintf("reading file: %v", err)}, nil
	}
	return Result{Output: string(contents)}, nil
}

type WriteFileTool struct{ workspaceTool }

func NewWriteFileTool(workspaceDirectory string) *WriteFileTool {
	return &WriteFileTool{workspaceTool{workspaceDirectory}}
}
func (tool *WriteFileTool) Name() string        { return "write_file" }
func (tool *WriteFileTool) Description() string { return "Write content to a file in /workspace, creating it if it doesn't exist." }
func (tool *WriteFileTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path relative to /workspace"},
			"content": map[string]any{"type": "string", "description": "Content to write"},
		},
		"required": []string{"path", "content"},
	}
}
func (tool *WriteFileTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	path, _ := arguments["path"].(string)
	content, _ := arguments["content"].(string)
	absolute, err := tool.resolvePath(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0755); err != nil {
		return Result{Error: fmt.Sprintf("creating directories: %v", err)}, nil
	}
	if err := os.WriteFile(absolute, []byte(content), 0644); err != nil {
		return Result{Error: fmt.Sprintf("writing file: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}, nil
}

type EditFileTool struct{ workspaceTool }

func NewEditFileTool(workspaceDirectory string) *EditFileTool {
	return &EditFileTool{workspaceTool{workspaceDirectory}}
}
func (tool *EditFileTool) Name() string { return "edit_file" }
func (tool *EditFileTool) Description() string {
	return "Replace an exact string in a file. Fails if the search text appears more than once."
}
func (tool *EditFileTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":     map[string]any{"type": "string", "description": "File path relative to /workspace"},
			"old_text": map[string]any{"type": "string", "description": "Exact text to find"},
			"new_text": map[string]any{"type": "string", "description": "Replacement text"},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}
func (tool *EditFileTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	path, _ := arguments["path"].(string)
	oldText, _ := arguments["old_text"].(string)
	newText, _ := arguments["new_text"].(string)
	absolute, err := tool.resolvePath(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	contents, err := os.ReadFile(absolute)
	if err != nil {
		return Result{Error: fmt.Sprintf("reading file: %v", err)}, nil
	}
	original := string(contents)
	count := strings.Count(original, oldText)
	if count == 0 {
		return Result{Error: "old_text not found in file"}, nil
	}
	if count > 1 {
		return Result{Error: fmt.Sprintf("old_text appears %d times — provide more context to make it unique", count)}, nil
	}
	updated := strings.Replace(original, oldText, newText, 1)
	if err := os.WriteFile(absolute, []byte(updated), 0644); err != nil {
		return Result{Error: fmt.Sprintf("writing file: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("edited %s", path)}, nil
}

type ListDirTool struct{ workspaceTool }

func NewListDirTool(workspaceDirectory string) *ListDirTool {
	return &ListDirTool{workspaceTool{workspaceDirectory}}
}
func (tool *ListDirTool) Name() string        { return "list_directory" }
func (tool *ListDirTool) Description() string { return "List the contents of a directory in /workspace." }
func (tool *ListDirTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Directory path relative to /workspace, defaults to root"},
		},
	}
}
func (tool *ListDirTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	path, _ := arguments["path"].(string)
	if path == "" {
		path = "."
	}
	absolute, err := tool.resolvePath(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return Result{Error: fmt.Sprintf("listing directory: %v", err)}, nil
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	return Result{Output: strings.Join(lines, "\n")}, nil
}

type AppendFileTool struct{ workspaceTool }

func NewAppendFileTool(workspaceDirectory string) *AppendFileTool {
	return &AppendFileTool{workspaceTool{workspaceDirectory}}
}
func (tool *AppendFileTool) Name() string        { return "append_file" }
func (tool *AppendFileTool) Description() string { return "Append content to the end of a file in /workspace." }
func (tool *AppendFileTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path relative to /workspace"},
			"content": map[string]any{"type": "string", "description": "Content to append"},
		},
		"required": []string{"path", "content"},
	}
}
func (tool *AppendFileTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	path, _ := arguments["path"].(string)
	content, _ := arguments["content"].(string)
	absolute, err := tool.resolvePath(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0755); err != nil {
		return Result{Error: fmt.Sprintf("creating directories: %v", err)}, nil
	}
	file, err := os.OpenFile(absolute, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return Result{Error: fmt.Sprintf("opening file: %v", err)}, nil
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		return Result{Error: fmt.Sprintf("appending to file: %v", err)}, nil
	}
	return Result{Output: fmt.Sprintf("appended %d bytes to %s", len(content), path)}, nil
}
