package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupWorkspace(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	return directory
}

func TestReadFileTool(t *testing.T) {
	workspace := setupWorkspace(t)
	os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello world"), 0644)
	tool := NewReadFileTool(workspace)
	tests := []struct {
		name        string
		arguments   map[string]any
		wantOutput  string
		wantError   string
	}{
		{"reads file by relative path", map[string]any{"path": "hello.txt"}, "hello world", ""},
		{"reads file by /workspace/ prefix", map[string]any{"path": "/workspace/hello.txt"}, "hello world", ""},
		{"missing file", map[string]any{"path": "missing.txt"}, "", "reading file:"},
		{"path escape blocked", map[string]any{"path": "../../etc/passwd"}, "", "path escapes workspace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), test.arguments)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError != "" {
				if result.Error == "" || !contains(result.Error, test.wantError) {
					t.Errorf("expected error containing %q, got %q", test.wantError, result.Error)
				}
				return
			}
			if result.Output != test.wantOutput {
				t.Errorf("expected output %q, got %q", test.wantOutput, result.Output)
			}
		})
	}
}

func TestWriteFileTool(t *testing.T) {
	workspace := setupWorkspace(t)
	tool := NewWriteFileTool(workspace)
	tests := []struct {
		name      string
		arguments map[string]any
		wantError string
		checkFile string
		checkBody string
	}{
		{"writes new file", map[string]any{"path": "new.txt", "content": "content"}, "", "new.txt", "content"},
		{"creates parent dirs", map[string]any{"path": "sub/dir/file.txt", "content": "nested"}, "", "sub/dir/file.txt", "nested"},
		{"overwrites existing", map[string]any{"path": "new.txt", "content": "updated"}, "", "new.txt", "updated"},
		{"path escape blocked", map[string]any{"path": "../../evil.txt", "content": "x"}, "path escapes workspace", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), test.arguments)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError != "" {
				if !contains(result.Error, test.wantError) {
					t.Errorf("expected error containing %q, got %q", test.wantError, result.Error)
				}
				return
			}
			if test.checkFile != "" {
				body, err := os.ReadFile(filepath.Join(workspace, test.checkFile))
				if err != nil {
					t.Fatalf("checking written file: %v", err)
				}
				if string(body) != test.checkBody {
					t.Errorf("expected file content %q, got %q", test.checkBody, string(body))
				}
			}
		})
	}
}

func TestEditFileTool(t *testing.T) {
	workspace := setupWorkspace(t)
	tool := NewEditFileTool(workspace)
	os.WriteFile(filepath.Join(workspace, "doc.txt"), []byte("hello world"), 0644)
	os.WriteFile(filepath.Join(workspace, "dup.txt"), []byte("foo foo"), 0644)
	tests := []struct {
		name      string
		arguments map[string]any
		wantError string
		wantBody  string
	}{
		{"replaces unique text", map[string]any{"path": "doc.txt", "old_text": "hello", "new_text": "goodbye"}, "", "goodbye world"},
		{"fails on duplicate match", map[string]any{"path": "dup.txt", "old_text": "foo", "new_text": "bar"}, "appears 2 times", ""},
		{"fails when not found", map[string]any{"path": "doc.txt", "old_text": "missing", "new_text": "x"}, "not found", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), test.arguments)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError != "" {
				if !contains(result.Error, test.wantError) {
					t.Errorf("expected error containing %q, got %q", test.wantError, result.Error)
				}
				return
			}
			body, _ := os.ReadFile(filepath.Join(workspace, test.arguments["path"].(string)))
			if string(body) != test.wantBody {
				t.Errorf("expected %q, got %q", test.wantBody, string(body))
			}
		})
	}
}

func TestListDirTool(t *testing.T) {
	workspace := setupWorkspace(t)
	os.WriteFile(filepath.Join(workspace, "a.txt"), []byte(""), 0644)
	os.Mkdir(filepath.Join(workspace, "subdir"), 0755)
	tool := NewListDirTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})
	if err != nil || result.Error != "" {
		t.Fatalf("unexpected error: %v %s", err, result.Error)
	}
	if !contains(result.Output, "a.txt") {
		t.Error("expected a.txt in output")
	}
	if !contains(result.Output, "subdir/") {
		t.Error("expected subdir/ in output")
	}
}

func TestAppendFileTool(t *testing.T) {
	workspace := setupWorkspace(t)
	tool := NewAppendFileTool(workspace)
	tool.Execute(context.Background(), map[string]any{"path": "log.txt", "content": "line1\n"})
	tool.Execute(context.Background(), map[string]any{"path": "log.txt", "content": "line2\n"})
	body, err := os.ReadFile(filepath.Join(workspace, "log.txt"))
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(body) != "line1\nline2\n" {
		t.Errorf("expected appended content, got %q", string(body))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
