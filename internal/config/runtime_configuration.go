package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type RuntimeConfiguration struct {
	BaseURL       string                      `json:"baseURL"`
	Capabilities  CapabilityConfiguration     `json:"capabilities"`
	AgentProfiles []AgentProfileConfiguration `json:"agentProfiles"`
	LanguageModel LanguageModelConfiguration  `json:"languageModel"`
	Firecracker   FirecrackerConfiguration    `json:"firecracker"`
	Bridge        BridgeConfiguration         `json:"bridge"`
	Database      DatabaseConfiguration       `json:"database"`
	Memory        MemoryConfiguration         `json:"memory"`
	Agent         AgentConfiguration          `json:"agent"`
	Connectors    ConnectorConfiguration      `json:"connectors"`
	Logging       LoggingConfiguration        `json:"logging"`
	MCPServers    []MCPServerConfiguration    `json:"mcpServers"`
	Terminal      TerminalConfiguration       `json:"terminal"`
	Scheduler     SchedulerConfiguration      `json:"scheduler"`
}

type CapabilityConfiguration struct {
	Endpoint              string                     `json:"endpoint"`
	Transport             string                     `json:"transport"`
	UnixSocketPath        string                     `json:"unixSocketPath"`
	TimeoutSecond         int                        `json:"timeoutSecond"`
	VSockCID              uint32                     `json:"vsockCID"`
	VSockPort             uint32                     `json:"vsockPort"`
	ProtocolVersion       string                     `json:"protocolVersion"`
	AggregateProtocolHash string                     `json:"aggregateProtocolHash"`
	ToolDescriptors       []CapabilityToolDescriptor `json:"toolDescriptors,omitempty"`
}

type CapabilityToolDescriptor struct {
	Name                 string                        `json:"name"`
	CanonicalName        string                        `json:"canonicalName"`
	Namespace            string                        `json:"namespace"`
	ModelName            string                        `json:"modelName"`
	ModelVisibility      string                        `json:"modelVisibility"`
	Description          string                        `json:"description,omitempty"`
	Version              string                        `json:"version,omitempty"`
	PrivacyClass         string                        `json:"privacyClass,omitempty"`
	EstimatedLatency     string                        `json:"estimatedLatency,omitempty"`
	RequiresUserPresence bool                          `json:"requiresUserPresence,omitempty"`
	WorksOffline         bool                          `json:"worksOffline,omitempty"`
	InputSchema          json.RawMessage               `json:"inputSchema,omitempty"`
	OutputSchema         json.RawMessage               `json:"outputSchema,omitempty"`
	ResultContract       *CapabilityToolResultContract `json:"resultContract,omitempty"`
	PolicyResource       string                        `json:"policyResource,omitempty"`
	SideEffectClass      string                        `json:"sideEffectClass,omitempty"`
	RequiresApproval     bool                          `json:"requiresApproval,omitempty"`
	CompletionEvidence   *CapabilityCompletionEvidence `json:"completionEvidence,omitempty"`
	Availability         CapabilityAvailability        `json:"availability"`
	Idempotency          CapabilityIdempotency         `json:"idempotency"`
}

type CapabilityToolResultContract struct {
	Schema  json.RawMessage                    `json:"schema"`
	Effects []CapabilityResourceEffectContract `json:"effects,omitempty"`
}

type CapabilityResourceEffectContract struct {
	ObjectType     string `json:"objectType"`
	Effect         string `json:"effect"`
	ResultField    string `json:"resultField"`
	EffectIdentity string `json:"effectIdentity"`
}

type CapabilityCompletionEvidence struct {
	Mode       string `json:"mode,omitempty"`
	Action     string `json:"action,omitempty"`
	TargetKind string `json:"targetKind,omitempty"`
}

type CapabilityAvailability struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type CapabilityIdempotency struct {
	Supported bool   `json:"supported"`
	Required  bool   `json:"required"`
	Scope     string `json:"scope,omitempty"`
}

type AgentProfileConfiguration struct {
	Name             string   `json:"name"`
	AllowedToolNames []string `json:"allowedToolNames"`
}

type MCPServerConfiguration struct {
	Name      string                 `json:"name"`
	Transport string                 `json:"transport"`
	Command   string                 `json:"command"`
	Arguments []string               `json:"arguments"`
	Endpoint  string                 `json:"endpoint"`
	Tools     []MCPToolConfiguration `json:"tools,omitempty"`
}

type MCPToolConfiguration struct {
	Name        string                 `json:"name"`
	Namespace   string                 `json:"namespace"`
	Description string                 `json:"description"`
	InputSchema json.RawMessage        `json:"inputSchema"`
	Policy      *MCPToolPolicyMetadata `json:"policy"`
}

type MCPToolPolicyMetadata struct {
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

type AgentConfiguration struct {
	Intake                   AgentIntakeConfiguration `json:"intake"`
	DefaultTaskLevel         string                   `json:"defaultTaskLevel"`
	SkillTaskLevelFloor      string                   `json:"skillTaskLevelFloor,omitempty"`
	ToolResultMaxBytes       int                      `json:"toolResultMaxBytes"`
	FailureRecovery          AgentFailureRecovery     `json:"failureRecovery"`
	GenerationOptions        AgentGenerationOptions   `json:"generationOptions,omitempty"`
	AdminTaskLinkBaseURL     string                   `json:"adminTaskLinkBaseURL,omitempty"`
	AllowAdminTaskDiagnostic bool                     `json:"allowAdminTaskDiagnostic"`
}

type AgentIntakeConfiguration struct {
	Enabled       bool   `json:"enabled"`
	Model         string `json:"model"`
	ExecutionMode string `json:"executionMode"`
}

type AgentFailureRecovery struct {
	FailureDebtFinalizationGate bool                `json:"failureDebtFinalizationGate"`
	AttemptFingerprint          string              `json:"attemptFingerprint"`
	RecoveryBudget              AgentRecoveryBudget `json:"recoveryBudget"`
}

type AgentRecoveryBudget struct {
	CorrectedRetry int `json:"correctedRetry"`
	AlternateRoute int `json:"alternateRoute"`
	AdjacentTool   int `json:"adjacentTool"`
	NoToolFallback int `json:"noToolFallback"`
}

type AgentGenerationOptions struct {
	Seed        *int64   `json:"seed,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type LanguageModelConfiguration struct {
	DefaultProvider  string                               `json:"defaultProvider"`
	FallbackProvider string                               `json:"fallbackProvider"`
	Capability       LanguageModelCapabilityConfiguration `json:"capability"`
	SDKD             LanguageModelSDKDConfiguration       `json:"sdkd"`
}

type LanguageModelSDKDConfiguration struct {
	Endpoint              string   `json:"endpoint"`
	UnixSocketPath        string   `json:"unixSocketPath"`
	AuthKeyPath           string   `json:"authKeyPath"`
	ExecutionMode         string   `json:"executionMode"`
	LocalOnly             bool     `json:"localOnly"`
	ShadowEnabled         bool     `json:"shadowEnabled"`
	StructuredSchemaNames []string `json:"structuredSchemaNames"`
}

type LanguageModelCapabilityConfiguration struct {
	Model               string `json:"model"`
	MaximumModelTier    string `json:"maximumModelTier,omitempty"`
	MaxModel            string `json:"maxModel"`
	XHighModel          string `json:"xhighModel"`
	HighModel           string `json:"highModel"`
	MediumModel         string `json:"mediumModel"`
	LowModel            string `json:"lowModel"`
	XLowModel           string `json:"xlowModel"`
	CodingModel         string `json:"codingModel"`
	ExecutionMode       string `json:"executionMode"`
	ContextWindowTokens int    `json:"contextWindowTokens"`
}

type FirecrackerConfiguration struct {
	FirecrackerPath        string                            `json:"firecrackerPath"`
	JailerPath             string                            `json:"jailerPath"`
	KernelImagePath        string                            `json:"kernelImagePath"`
	RootfsImagePath        string                            `json:"rootfsImagePath"`
	WorkspaceImagePath     string                            `json:"workspaceImagePath"`
	HostWorkspacePath      string                            `json:"hostWorkspacePath"`
	VCPUCount              int                               `json:"vcpuCount"`
	MemoryMiB              int                               `json:"memoryMiB"`
	VSockCID               uint32                            `json:"vsockCID"`
	HealthPortOrService    string                            `json:"healthPortOrService"`
	GuestHTTPPortOrService string                            `json:"guestHTTPPortOrService"`
	HostHTTPListenAddress  string                            `json:"hostHTTPListenAddress"`
	LogDirectoryPath       string                            `json:"logDirectoryPath"`
	RuntimeDirectoryPath   string                            `json:"runtimeDirectoryPath"`
	OutboundNetwork        OutboundNetworkConfiguration      `json:"outboundNetwork"`
	GuestListenerProxies   []GuestListenerProxyConfiguration `json:"guestListenerProxies"`
}

type OutboundNetworkConfiguration struct {
	Enabled          bool   `json:"enabled"`
	HostDeviceName   string `json:"hostDeviceName"`
	GuestMACAddress  string `json:"guestMACAddress"`
	NetworkCIDR      string `json:"networkCIDR"`
	HostAddressCIDR  string `json:"hostAddressCIDR"`
	GuestAddressCIDR string `json:"guestAddressCIDR"`
	GuestGateway     string `json:"guestGateway"`
}

type GuestListenerProxyConfiguration struct {
	GuestPort            uint32 `json:"guestPort"`
	TargetUnixSocketPath string `json:"targetUnixSocketPath"`
}

type BridgeConfiguration struct {
	Mode                     string `json:"mode"`
	AuthMode                 string `json:"authMode"`
	AuthorizedPublicKeysPath string `json:"authorizedPublicKeysPath"`
	ListenAddress            string `json:"listenAddress"`
}

type DatabaseConfiguration struct {
	Driver                 string `json:"driver"`
	ConnectionString       string `json:"connectionString"`
	MigrationDirectoryPath string `json:"migrationDirectoryPath"`
}

type MemoryConfiguration struct {
	WorkspaceID                                 string `json:"workspaceID"`
	GraphitiEndpoint                            string `json:"graphitiEndpoint"`
	GraphitiKuzuPath                            string `json:"graphitiKuzuPath"`
	PinnedMemoryRootPath                        string `json:"pinnedMemoryRootPath"`
	PinnedMemoryCharacterLimit                  int    `json:"pinnedMemoryCharacterLimit"`
	PinnedMemoryHardLimitCharacterCount         int    `json:"pinnedMemoryHardLimitCharacterCount"`
	PinnedMemoryCompressionTargetCharacterCount int    `json:"pinnedMemoryCompressionTargetCharacterCount"`
	TimeoutSecond                               int    `json:"timeoutSecond"`
}

type ConnectorConfiguration struct {
	Mattermost MattermostConnectorConfiguration `json:"mattermost"`
	Slack      SlackConnectorConfiguration      `json:"slack"`
	Signal     SignalConnectorConfiguration     `json:"signal"`
}

type MattermostConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SlackConnectorConfiguration struct {
	BaseURL string `json:"baseURL"`
}

type SignalConnectorConfiguration struct {
	Enabled bool `json:"enabled"`
}

type LoggingConfiguration struct {
	DirectoryPath string `json:"directoryPath"`
	RetentionDays int    `json:"retentionDays"`
}

type TerminalConfiguration struct {
	Mode                  string `json:"mode"`
	SandboxProvider       string `json:"sandboxProvider"`
	WorkspaceRootPath     string `json:"workspaceRootPath"`
	POSIXHelperPath       string `json:"posixHelperPath"`
	TimeoutSecond         int    `json:"timeoutSecond"`
	OutputMaxBytes        int    `json:"outputMaxBytes"`
	SessionMaxCount       int    `json:"sessionMaxCount"`
	AllowNetwork          bool   `json:"allowNetwork"`
	AllowInteractiveShell bool   `json:"allowInteractiveShell"`
}

type SchedulerConfiguration struct {
	RetentionCheckIntervalMinute   int `json:"retentionCheckIntervalMinute"`
	TaskSchedulePollIntervalSecond int `json:"taskSchedulePollIntervalSecond"`
	TaskRetentionDays              int `json:"taskRetentionDays"`
}

func LoadRuntimeConfiguration(path string) (RuntimeConfiguration, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	var configuration RuntimeConfiguration
	errorValue = json.Unmarshal(document, &configuration)
	if errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}
	if errorValue := validateCapabilityProtocolIdentity(&configuration.Capabilities); errorValue != nil {
		return RuntimeConfiguration{}, errorValue
	}

	return configuration, nil
}

func validateCapabilityProtocolIdentity(configuration *CapabilityConfiguration) error {
	if configuration.ProtocolVersion == "" || configuration.ProtocolVersion != strings.TrimSpace(configuration.ProtocolVersion) {
		return fmt.Errorf("capabilities.protocolVersion must be a non-empty trimmed string")
	}
	if configuration.AggregateProtocolHash != strings.TrimSpace(configuration.AggregateProtocolHash) {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	if len(configuration.AggregateProtocolHash) != 64 {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	if _, errorValue := hex.DecodeString(configuration.AggregateProtocolHash); errorValue != nil || strings.ToLower(configuration.AggregateProtocolHash) != configuration.AggregateProtocolHash {
		return fmt.Errorf("capabilities.aggregateProtocolHash must be a 64-character lowercase hexadecimal hash")
	}
	return nil
}
