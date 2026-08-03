package harnessselection

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/acpharness"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
)

const (
	BundledHarnessName  = "bluecollar"
	ExternalHarnessName = "acp"
)

type ToolCatalogEndpoint struct {
	URL      string
	Resolver *mcpserver.SessionTokenRequesterResolver
	Handler  http.Handler
}

func Select(harnessConfiguration config.HarnessConfiguration, bundledHarnessFactory harnessdriver.Factory, toolCatalogEndpoint ToolCatalogEndpoint) (harnessdriver.Factory, error) {
	harnessName := strings.TrimSpace(harnessConfiguration.Name)
	switch harnessName {
	case "", BundledHarnessName:
		if bundledHarnessFactory == nil {
			return nil, fmt.Errorf("no harness is configured and this build ships none; set agent.harness.name to %q with an agent command", ExternalHarnessName)
		}
		return bundledHarnessFactory, nil
	case ExternalHarnessName:
		return externalHarnessFactory(harnessConfiguration, toolCatalogEndpoint)
	default:
		return nil, fmt.Errorf("unknown harness %q; known harnesses are %q and %q", harnessName, BundledHarnessName, ExternalHarnessName)
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
	publisher := sessionTokenPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		return acpharness.New(agentCommand, publisher, dependencies.TaskRunStore), nil
	}, nil
}

type sessionTokenPublisher struct {
	endpointURL string
	resolver    *mcpserver.SessionTokenRequesterResolver
}

func (publisher sessionTokenPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet.RequesterPersonID, requesterToolSet.ToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}
