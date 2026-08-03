package harnessselection

import (
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/harnessdriver"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
)

func bundledFactory() harnessdriver.Factory {
	return func(harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		return nil, nil
	}
}

func publishedCatalog() ToolCatalogEndpoint {
	return ToolCatalogEndpoint{URL: "http://127.0.0.1:0/tools", Resolver: mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })}
}

func TestSelectFallsBackToTheBundledHarness(t *testing.T) {
	for _, harnessName := range []string{"", BundledHarnessName} {
		selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: harnessName}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{})
		if errorValue != nil || selectedFactory == nil {
			t.Fatalf("expected the bundled harness for %q, got %v", harnessName, errorValue)
		}
	}
}

func TestSelectFailsLoudlyWhenNoHarnessIsAvailable(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{}, nil, publishedCatalog(), SandboxProcessBoundary{})
	if errorValue == nil || !strings.Contains(errorValue.Error(), ExternalHarnessName) {
		t.Fatalf("expected a build with no bundled harness to say how to configure one, got %v", errorValue)
	}
}

func TestSelectRejectsAnUnknownHarnessRatherThanIgnoringIt(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{Name: "claude-code"}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "claude-code") {
		t.Fatalf("expected an unknown harness name to be refused by name, got %v", errorValue)
	}
}

func TestSelectRefusesAnExternalHarnessThatWouldHaveNoToolsOrNoAgent(t *testing.T) {
	if _, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected an external harness with no agent command to be refused")
	}
	_, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"}, bundledFactory(), ToolCatalogEndpoint{}, SandboxProcessBoundary{})
	if errorValue == nil {
		t.Fatal("expected an external harness with no published tool catalog to be refused, because it would have no tools it may run as the requester")
	}
}

func TestSelectBuildsTheExternalHarnessWhenBothAreConfigured(t *testing.T) {
	selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{})
	if errorValue != nil {
		t.Fatalf("expected a configured external harness: %v", errorValue)
	}
	harness, _ := selectedFactory(harnessdriver.Dependencies{})
	if harness == nil {
		t.Fatal("expected the external harness to be constructed")
	}
}

func TestPublishedCatalogGrantsAndRevokesPerTurn(t *testing.T) {
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	publisher := sessionTokenPublisher{endpointURL: "http://127.0.0.1:0/tools", resolver: resolver}

	_, sessionToken, revoke, errorValue := publisher.PublishToolCatalog(mcpserver.RequesterToolSet{RequesterPersonID: "person-1", ToolSet: emptyToolSet()})
	if errorValue != nil {
		t.Fatalf("expected a tool catalog grant: %v", errorValue)
	}
	if _, errorValue := resolver.ResolveRequester(sessionToken); errorValue != nil {
		t.Fatalf("expected the granted token to resolve: %v", errorValue)
	}
	revoke()
	if _, errorValue := resolver.ResolveRequester(sessionToken); errorValue == nil {
		t.Fatal("expected the token to stop resolving once the turn ended")
	}
}

func TestClaudeCodeIsSelectableAndDeniesItsOwnBuiltinTools(t *testing.T) {
	if _, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected claude-code with no executable path to be refused")
	}
	if _, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName, AgentCommandPath: "/usr/bin/true"}, bundledFactory(), ToolCatalogEndpoint{}, SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected claude-code with no tool catalog to be refused")
	}
	selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName, AgentCommandPath: "/usr/bin/true"}, bundledFactory(), publishedCatalog(), SandboxProcessBoundary{})
	if errorValue != nil {
		t.Fatalf("expected a configured claude-code harness: %v", errorValue)
	}
	if harness, _ := selectedFactory(harnessdriver.Dependencies{}); harness == nil {
		t.Fatal("expected the claude-code harness to be constructed")
	}
	for _, builtinToolName := range []string{"Bash", "Read", "Write", "Edit"} {
		if !containsToolName(claudeCodeBuiltinToolNames, builtinToolName) {
			t.Fatalf("expected %q to be denied so the agent cannot execute outside the sandbox", builtinToolName)
		}
	}
}

func containsToolName(toolNames []string, candidate string) bool {
	for _, toolName := range toolNames {
		if toolName == candidate {
			return true
		}
	}
	return false
}
