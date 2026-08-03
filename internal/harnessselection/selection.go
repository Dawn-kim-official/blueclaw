package harnessselection

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/acpharness"
	"github.com/Dawn-kim-official/blueclaw/internal/cliharness"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

const (
	BundledHarnessName    = "bluecollar"
	ExternalHarnessName   = "acp"
	ClaudeCodeHarnessName = "claude-code"
	CodexHarnessName      = "codex"

	ClaudeCodeAgentCommandName = "claude"
	CodexAgentCommandName      = "codex"
)

type ToolCatalogEndpoint struct {
	URL          string
	Resolver     *mcpserver.SessionTokenRequesterResolver
	Handler      http.Handler
	ApprovalGate mcpserver.ApprovalGate
}

type RequesterProcessRunner interface {
	Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error)
}

type SandboxProcessBoundary struct {
	Runner            RequesterProcessRunner
	WorkspaceRootPath string
}

func Select(harnessConfiguration config.HarnessConfiguration, bundledHarnessFactory harnessdriver.Factory, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	harnessName := strings.TrimSpace(harnessConfiguration.Name)
	switch harnessName {
	case "", BundledHarnessName:
		if bundledHarnessFactory == nil {
			return nil, fmt.Errorf("no agent harness is attached and this build ships none; install %s or %s, then set agent.harness.name to %q or %q and agent.harness.agentCommandPath to that executable", ClaudeCodeAgentCommandName, CodexAgentCommandName, ClaudeCodeHarnessName, CodexHarnessName)
		}
		return bundledHarnessFactory, nil
	case ExternalHarnessName:
		return externalHarnessFactory(harnessConfiguration, toolCatalogEndpoint)
	case ClaudeCodeHarnessName:
		return commandHarnessFactory(ClaudeCodeHarnessName, cliharness.ClaudeCodeAgentCommand(strings.TrimSpace(harnessConfiguration.AgentCommandPath)), harnessConfiguration, toolCatalogEndpoint, processBoundary)
	case CodexHarnessName:
		return commandHarnessFactory(CodexHarnessName, cliharness.CodexAgentCommand(strings.TrimSpace(harnessConfiguration.AgentCommandPath)), harnessConfiguration, toolCatalogEndpoint, processBoundary)
	default:
		return nil, fmt.Errorf("unknown harness %q; known harnesses are %q, %q, %q and %q", harnessName, BundledHarnessName, ExternalHarnessName, ClaudeCodeHarnessName, CodexHarnessName)
	}
}

func externalHarnessFactory(harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint) (harnessdriver.Factory, error) {
	if strings.TrimSpace(harnessConfiguration.AgentCommandPath) == "" {
		return nil, fmt.Errorf("harness %q needs agent.harness.agentCommandPath, the ACP agent to run", ExternalHarnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools it may run as the requester", ExternalHarnessName)
	}
	agentCommand := acpharness.AgentCommand{
		Path:      harnessConfiguration.AgentCommandPath,
		Arguments: append([]string{}, harnessConfiguration.AgentArguments...),
	}
	publisher := sessionTokenPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver, approvalGate: toolCatalogEndpoint.ApprovalGate}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		return acpharness.New(agentCommand, publisher, dependencies.TaskRunStore), nil
	}, nil
}

type sessionTokenPublisher struct {
	endpointURL  string
	resolver     *mcpserver.SessionTokenRequesterResolver
	approvalGate mcpserver.ApprovalGate
}

func (publisher sessionTokenPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	requesterToolSet.ApprovalGate = publisher.approvalGate
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}

func commandHarnessFactory(harnessName string, agentCommand cliharness.AgentCommand, harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	if strings.TrimSpace(harnessConfiguration.AgentCommandPath) == "" {
		return nil, fmt.Errorf("harness %q needs agent.harness.agentCommandPath, the executable to run", harnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools it may run as the requester", harnessName)
	}
	if processBoundary.Runner == nil {
		return nil, fmt.Errorf("harness %q may only run inside the requester's POSIX identity, because it brings tools of its own that the kernel rather than a deny list has to confine; configure the terminal boundary first", harnessName)
	}
	publisher := sessionTokenPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver, approvalGate: toolCatalogEndpoint.ApprovalGate}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		harness := cliharness.New(agentCommand, publisher, dependencies.TaskRunStore)
		if processBoundary.Runner != nil {
			harness.UseRequesterProcessRunner(processBoundary.Runner, processBoundary.WorkspaceRootPath)
		}
		return harness, nil
	}, nil
}
