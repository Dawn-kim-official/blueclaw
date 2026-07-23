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
	Name              string              `json:"name"`
	Namespace         string              `json:"namespace"`
	ServerName        string              `json:"serverName"`
	Description       string              `json:"description"`
	InputSchema       json.RawMessage     `json:"inputSchema"`
	InputIntentSchema json.RawMessage     `json:"inputIntentSchema,omitempty"`
	OutputSchema      json.RawMessage     `json:"outputSchema"`
	ResultContract    *ToolResultContract `json:"resultContract"`
	Policy            PolicyMetadata      `json:"policy"`
	remoteName        string
}

type ToolResultContract struct {
	Schema            json.RawMessage          `json:"schema"`
	Effects           []ResourceEffectContract `json:"effects,omitempty"`
	EvidenceCondition *EvidenceCondition       `json:"evidenceCondition,omitempty"`
}

type EvidenceCondition struct {
	ResultField string          `json:"resultField"`
	Equals      json.RawMessage `json:"equals"`
}

type ResourceEffectContract struct {
	ObjectType     string `json:"objectType"`
	Effect         string `json:"effect"`
	ResultField    string `json:"resultField"`
	EffectIdentity string `json:"effectIdentity"`
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
	IdempotencyScope     string `json:"idempotencyScope"`
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
