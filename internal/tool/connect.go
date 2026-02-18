package tool

import (
	"context"
	"fmt"

	"github.com/blueclaw/blueclaw/internal/memory"
)

type ConnectTool struct {
	graphStore *memory.GraphStore
}

func NewConnectTool(graphStore *memory.GraphStore) *ConnectTool {
	return &ConnectTool{graphStore: graphStore}
}

func (tool *ConnectTool) Name() string { return "connect" }

func (tool *ConnectTool) Description() string {
	return "Create a directed, labeled relationship between two memories. Use relation 'updates', 'extends', or 'derives'."
}

func (tool *ConnectTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from_title": map[string]any{
				"type":        "string",
				"description": "Title of the source memory",
			},
			"to_title": map[string]any{
				"type":        "string",
				"description": "Title of the target memory",
			},
			"relation": map[string]any{
				"type":        "string",
				"description": "Relationship label (e.g. 'updates', 'extends', 'derives')",
			},
		},
		"required": []string{"from_title", "to_title", "relation"},
	}
}

func (tool *ConnectTool) Execute(_ context.Context, arguments map[string]any) (Result, error) {
	fromTitle, _ := arguments["from_title"].(string)
	toTitle, _ := arguments["to_title"].(string)
	relation, _ := arguments["relation"].(string)
	if fromTitle == "" {
		return Result{Error: "from_title is required"}, nil
	}
	if toTitle == "" {
		return Result{Error: "to_title is required"}, nil
	}
	if relation == "" {
		return Result{Error: "relation is required"}, nil
	}
	if fromTitle == toTitle {
		return Result{Error: "cannot connect a memory to itself"}, nil
	}
	from, err := tool.graphStore.Load(fromTitle)
	if err != nil {
		return Result{Error: fmt.Sprintf("from_title not found: %s", fromTitle)}, nil
	}
	to, err := tool.graphStore.Load(toTitle)
	if err != nil {
		return Result{Error: fmt.Sprintf("to_title not found: %s", toTitle)}, nil
	}
	if err := tool.graphStore.Connect(from.ID, to.ID, relation); err != nil {
		return Result{}, fmt.Errorf("connecting memories: %w", err)
	}
	return Result{Output: fmt.Sprintf("connected: %s -[%s]-> %s", fromTitle, relation, toTitle)}, nil
}
