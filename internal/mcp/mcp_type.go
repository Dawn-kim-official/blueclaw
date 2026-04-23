package mcp

type ServerDefinition struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
	Endpoint  string   `json:"endpoint"`
	ToolNames []string `json:"toolNames"`
}

type ToolDefinition struct {
	Name       string `json:"name"`
	ServerName string `json:"serverName"`
}

type Invocation struct {
	ServerName string `json:"serverName"`
	ToolName   string `json:"toolName"`
	Input      string `json:"input"`
}
