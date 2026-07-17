package mcp

import "encoding/json"

const (
	TransportStdio          = "stdio"
	TransportStreamableHTTP = "streamable_http"
)

type ServerDefinition struct {
	Name      string           `json:"name"`
	Transport string           `json:"transport"`
	Command   string           `json:"command"`
	Arguments []string         `json:"arguments"`
	Endpoint  string           `json:"endpoint"`
	Tools     []ToolDefinition `json:"tools,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace"`
	ServerName  string          `json:"serverName"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Policy      PolicyMetadata  `json:"policy"`
	remoteName  string
}

type PolicyMetadata struct {
	PrivacyClass         string `json:"privacyClass"`
	RequiresUserPresence bool   `json:"requiresUserPresence"`
	WorksOffline         bool   `json:"worksOffline"`
	ModelVisibility      string `json:"modelVisibility"`
	PolicyResource       string `json:"policyResource"`
	SideEffectClass      string `json:"sideEffectClass"`
	RequiresApproval     bool   `json:"requiresApproval"`
	CompletionMode       string `json:"completionMode"`
	CompletionAction     string `json:"completionAction"`
	CompletionTargetKind string `json:"completionTargetKind"`
	Idempotency          string `json:"idempotency"`
}

type Invocation struct {
	ServerName string `json:"serverName"`
	ToolName   string `json:"toolName"`
	Input      string `json:"input"`
}

type LoadReport struct {
	Quarantined []QuarantinedServer `json:"quarantined"`
}

type QuarantinedServer struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
