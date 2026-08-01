package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
	"testing"
)

func TestCanonicalExpectedResultURLsIgnoreUncontractedOutput(t *testing.T) {
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{"external.publish"})}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Tool:          "external.publish",
		Output: toolcontract.ToolOutput{
			Content: `{"publicURL":"https://portfolio.example"}`,
			Data:    json.RawMessage(`{}`),
		},
	}}

	if urls := canonicalExpectedResultURLs(request, observations); len(urls) != 0 {
		t.Fatalf("uncontracted URL must not count as delivered link: %+v", urls)
	}
}

func TestCanonicalExpectedResultURLsUseValidatedEffects(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("external.publish", "https://portfolio.example")
	request := AgentTurnRequest{ToolSet: toolSet}

	if urls := canonicalExpectedResultURLs(request, []turnObservation{observation}); strings.Join(urls, ",") != "https://portfolio.example" {
		t.Fatalf("expected exact canonical URL, got %+v", urls)
	}

	observation.Effects[0].URL = "https://different.example"
	if urls := canonicalExpectedResultURLs(request, []turnObservation{observation}); len(urls) != 0 {
		t.Fatalf("mismatched effect identity must fail closed: %+v", urls)
	}
}

func TestCanonicalExpectedResultURLsPreferRequiredEffectIdentity(t *testing.T) {
	searchDefinition := canonicalLinkToolDefinition("web.search")
	searchDefinition.ResultContract.Effects[0].ObjectType = "reference"
	searchDefinition.ResultContract.Effects[0].Effect = "found"
	searchResult := canonicalLinkToolResult("https://reference.example")
	searchResult.Effects[0].ObjectType = "reference"
	searchResult.Effects[0].Effect = "found"
	searchObservation := turnObservation{
		ObservationID: "obs-001",
		Tool:          "web.search",
		Output:        searchResult.Output,
		Effects:       searchResult.Effects,
	}
	publishToolSet, publishObservation := canonicalLinkObservation("site.serve", "https://portfolio.example")
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		searchDefinition,
		mustToolDefinition(t, publishToolSet, "site.serve"),
	})
	request := AgentTurnRequest{
		ToolSet: toolSet,
		OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
			ObjectType: "website",
			Effect:     "published",
		}}},
	}

	if urls := canonicalExpectedResultURLs(request, []turnObservation{searchObservation, publishObservation}); strings.Join(urls, ",") != "https://portfolio.example" {
		t.Fatalf("expected required publish effect URL only, got %+v", urls)
	}
}

func TestExpectedResultDeliveryRequiresExactCanonicalURL(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("site.serve", "https://portfolio.example")
	request := AgentTurnRequest{
		ToolSet: toolSet,
		OutcomeContract: OutcomeContract{
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "published website URL",
				Required:    true,
			}},
		},
	}

	wrongURL := validateExpectedResultDelivery(request, []turnObservation{observation}, nil, finishDocument("Deployed it: https://different.example"))
	if wrongURL.IsSatisfied || !strings.Contains(wrongURL.Message, "https://portfolio.example") {
		t.Fatalf("expected exact observed URL requirement, got %+v", wrongURL)
	}

	exactURL := validateExpectedResultDelivery(request, []turnObservation{observation}, nil, finishDocument("Deployed it: https://portfolio.example/"))
	if !exactURL.IsSatisfied {
		t.Fatalf("expected normalized exact URL to pass, got %+v", exactURL)
	}
}

func TestExpectedResultDeliveryRequiresTypedFileAndMessage(t *testing.T) {
	request := AgentTurnRequest{OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{
		{ID: "attached-file", Type: ExpectedResultTypeFile, Description: "attached report", Required: true},
		{ID: "final-message", Type: ExpectedResultTypeMessage, Description: "final user reply", Required: true},
	}}}

	missingFile := validateExpectedResultDelivery(request, nil, nil, finishDocument("Prepared the file."))
	if missingFile.IsSatisfied {
		t.Fatal("expected missing typed attachment to block delivery")
	}

	ready := validateExpectedResultDelivery(request, nil, []toolcontract.FileAttachment{{Filename: "report.json"}}, finishDocument("Attached the file."))
	if !ready.IsSatisfied {
		t.Fatalf("expected typed attachment and message to pass, got %+v", ready)
	}
}

func canonicalLinkObservation(toolName string, publicURL string) (*toolcontract.ToolSet, turnObservation) {
	descriptor := canonicalLinkToolDefinition(toolName)
	result := canonicalLinkToolResult(publicURL)
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor}), turnObservation{
		ObservationID: "obs-001",
		Tool:          toolName,
		Output:        result.Output,
		Effects:       result.Effects,
	}
}

func canonicalLinkToolDefinition(toolName string) toolcontract.ToolDefinition {
	descriptor := testToolDescriptor(toolName)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`)
	descriptor.ResultContract = &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{
			ObjectType:     "website",
			Effect:         "published",
			ResultField:    "publicURL",
			EffectIdentity: "url",
		}},
	}
	return descriptor
}

func canonicalLinkToolResult(publicURL string) toolcontract.ToolResult {
	outputData := json.RawMessage(marshalEventBody(map[string]string{"publicURL": publicURL}))
	return toolcontract.ToolResult{
		Output:  toolcontract.ToolOutput{Content: string(outputData), Data: outputData},
		Effects: []toolcontract.ResourceEffect{{ObjectType: "website", Effect: "published", URL: publicURL}},
	}
}

func mustToolDefinition(t *testing.T, toolSet *toolcontract.ToolSet, toolName string) toolcontract.ToolDefinition {
	t.Helper()
	definition, isFound := toolSet.ToolDefinition(toolName)
	if !isFound {
		t.Fatalf("expected tool definition %s", toolName)
	}
	return definition
}

func finishDocument(message string) turnActionDocument {
	return turnActionDocument{Action: "finish", Message: message}
}

func TestLinkExpectationWithoutLinkCapableToolDoesNotHardBlock(t *testing.T) {
	toolSet := newTestToolSet([]string{"task.list"})
	expectation := ExpectedResult{Type: ExpectedResultTypeLink, Required: true}

	if message := missingExpectedResultDelivery(expectation, toolSet, nil, nil, "Here are the results."); message != "" {
		t.Fatalf("expected an unsatisfiable link expectation to defer to the judge, got %q", message)
	}

	linkToolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{canonicalLinkToolDefinition("site.serve")})
	if message := missingExpectedResultDelivery(expectation, linkToolSet, nil, nil, "Created it."); message == "" {
		t.Fatal("expected a link-capable working set to keep requiring the canonical link")
	}
}
