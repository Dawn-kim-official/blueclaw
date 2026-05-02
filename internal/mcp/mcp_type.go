package mcp

import "encoding/json"

type ServerDefinition struct {
	Name      string           `json:"name"`
	Transport string           `json:"transport"`
	Command   string           `json:"command"`
	Arguments []string         `json:"arguments"`
	Endpoint  string           `json:"endpoint"`
	ToolNames []string         `json:"toolNames"`
	Tools     []ToolDefinition `json:"tools,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	ServerName  string          `json:"serverName"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type Invocation struct {
	ServerName string `json:"serverName"`
	ToolName   string `json:"toolName"`
	Input      string `json:"input"`
}
