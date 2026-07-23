package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"blueclaw/internal/agent"
)

type recordingSkillSearchRetriever struct {
	instructions []agent.SkillInstruction
	limit        int
	candidates   []agent.SkillCandidate
}

func (retriever *recordingSkillSearchRetriever) Retrieve(context.Context, agent.AgentRequest, []agent.SkillInstruction, int) agent.SkillRetrievalResult {
	return agent.SkillRetrievalResult{}
}

func (retriever *recordingSkillSearchRetriever) Search(_ context.Context, _ agent.AgentRequest, instructions []agent.SkillInstruction, _ agent.SkillSearchQuerySet, limit int) agent.SkillRetrievalResult {
	retriever.instructions = append([]agent.SkillInstruction{}, instructions...)
	retriever.limit = limit
	return agent.SkillRetrievalResult{
		CandidateCount:     len(retriever.candidates),
		SelectedCandidates: append([]agent.SkillCandidate{}, retriever.candidates...),
	}
}

func (retriever *recordingSkillSearchRetriever) Refresh(context.Context, []agent.SkillInstruction) {}

func TestSkillSearchListModeIsBoundedAndStructured(t *testing.T) {
	instructions := make([]agent.SkillInstruction, 0, 25)
	for index := 0; index < 25; index++ {
		instructions = append(instructions, agent.SkillInstruction{
			Name:        fmt.Sprintf("skill-%02d", index),
			Description: fmt.Sprintf("Skill %02d", index),
		})
	}
	toolSet := canonicalSkillSearchToolSet(&recordingSkillSearchRetriever{}, instructions)

	result := invokeSkillSearch(t, toolSet, json.RawMessage(`{}`))

	if result.Mode != skillSearchModeList || len(result.Skills) != 20 || result.TotalCount != 25 || !result.HasMore {
		t.Fatalf("expected bounded list result, got %+v", result)
	}
	for _, skill := range result.Skills {
		if skill.ToolReferences == nil {
			t.Fatalf("expected normalized tool references, got %+v", skill)
		}
		if skill.Prompt != "" || skill.SourcePath != "" || skill.PromptTruncated != nil {
			t.Fatalf("expected list items to omit name-only fields, got %+v", skill)
		}
	}
}

func TestSkillSearchSearchModeCapsRetrieverAndPublicResults(t *testing.T) {
	instructions := make([]agent.SkillInstruction, 0, 10)
	candidates := make([]agent.SkillCandidate, 0, 10)
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("skill-%02d", index)
		instructions = append(instructions, agent.SkillInstruction{Name: name, Description: name})
		candidates = append(candidates, agent.SkillCandidate{Name: name, Score: float64(10 - index)})
	}
	retriever := &recordingSkillSearchRetriever{candidates: candidates}
	toolSet := canonicalSkillSearchToolSet(retriever, instructions)

	result := invokeSkillSearch(t, toolSet, json.RawMessage(`{"queries":[{"description":"Find a useful skill"}],"limit":20}`))

	if retriever.limit != 8 || len(result.Skills) != 8 {
		t.Fatalf("expected search bound of eight, retrieverLimit=%d result=%+v", retriever.limit, result)
	}
	if result.Mode != skillSearchModeSearch || result.TotalCount != 8 || result.HasMore {
		t.Fatalf("expected structured search pagination, got %+v", result)
	}
	document := decodeSkillSearchDocument(t, result)
	firstSkill := document["skills"].([]any)[0].(map[string]any)
	assertExactKeys(t, firstSkill, "name", "description", "toolReferences")
}

func TestSkillSearchSearchModeSlicesBoundedMatchesByRequestedLimit(t *testing.T) {
	instructions := make([]agent.SkillInstruction, 0, 10)
	candidates := make([]agent.SkillCandidate, 0, 10)
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("skill-%02d", index)
		instructions = append(instructions, agent.SkillInstruction{Name: name, Description: name})
		candidates = append(candidates, agent.SkillCandidate{Name: name, Score: float64(10 - index)})
	}
	retriever := &recordingSkillSearchRetriever{candidates: candidates}
	toolSet := canonicalSkillSearchToolSet(retriever, instructions)

	result := invokeSkillSearch(t, toolSet, json.RawMessage(`{"queries":[{"description":"Find a useful skill"}],"limit":2}`))

	if retriever.limit != 8 || result.TotalCount != 8 || len(result.Skills) != 2 || !result.HasMore {
		t.Fatalf("expected two of eight bounded matches, retrieverLimit=%d result=%+v", retriever.limit, result)
	}
}

func TestSkillSearchNameModeReturnsCanonicalPromptMetadata(t *testing.T) {
	longPrompt := strings.Repeat("가", maximumSkillSearchPromptLength+1)
	toolSet := canonicalSkillSearchToolSet(&recordingSkillSearchRetriever{}, []agent.SkillInstruction{{
		Name:           "site-prototype",
		Description:    "Create sites.",
		Prompt:         longPrompt,
		ToolReferences: []string{"file.read"},
		Source:         agent.InstructionSource{Path: "/host/private/workspace/skills/site-prototype/SKILL.md"},
	}})

	result := invokeSkillSearch(t, toolSet, json.RawMessage(`{"name":"SITE-PROTOTYPE"}`))

	if result.Mode != skillSearchModeName || len(result.Skills) != 1 || result.TotalCount != 1 || result.HasMore {
		t.Fatalf("expected one exact case-insensitive name result, got %+v", result)
	}
	skill := result.Skills[0]
	if len([]rune(skill.Prompt)) != maximumSkillSearchPromptLength || skill.PromptTruncated == nil || !*skill.PromptTruncated {
		t.Fatalf("expected explicit prompt truncation metadata, got promptRunes=%d item=%+v", len([]rune(skill.Prompt)), skill)
	}
	if strings.Contains(skill.Prompt, "skill.search truncated") {
		t.Fatalf("expected prompt without prose suffix, got %q", skill.Prompt)
	}
	if skill.SourcePath != "/workspace/skills/site-prototype/SKILL.md" {
		t.Fatalf("expected stable virtual source path, got %q", skill.SourcePath)
	}
}

func TestSkillSearchRejectsConflictingAndMalformedInputs(t *testing.T) {
	testCases := []json.RawMessage{
		json.RawMessage(`{"name":"mail","queries":[]}`),
		json.RawMessage(`{"name":"mail","limit":1}`),
		json.RawMessage(`{"name":"   "}`),
		json.RawMessage(`{"queries":[{"description":"   "}]}`),
		json.RawMessage(`{"queries":[{"description":"one"},{"description":"two"},{"description":"three"},{"description":"four"},{"description":"five"},{"description":"six"}]}`),
		json.RawMessage(`{"queries":[{"description":"mail","extra":true}]}`),
		json.RawMessage(`{"limit":0}`),
		json.RawMessage(`{"limit":21}`),
		json.RawMessage(`{"unknown":true}`),
	}
	for _, input := range testCases {
		t.Run(string(input), func(t *testing.T) {
			toolSet := canonicalSkillSearchToolSet(&recordingSkillSearchRetriever{}, nil)

			result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: agent.SkillSearchToolName,
				Input:    input,
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_input_schema" {
				t.Fatalf("expected strict input rejection, got %+v", result)
			}
		})
	}
}

func TestSkillSearchFiltersUnavailableToolReferencesBeforeEveryMode(t *testing.T) {
	instructions := []agent.SkillInstruction{
		{Name: "available", Description: "Available skill.", ToolReferences: []string{"file.read"}},
		{Name: "unavailable", Description: "Unavailable skill.", ToolReferences: []string{"missing.tool"}},
	}
	retriever := &recordingSkillSearchRetriever{candidates: []agent.SkillCandidate{
		{Name: "unavailable", Score: 1},
		{Name: "available", Score: 0.5},
	}}
	toolSet := canonicalSkillSearchToolSet(retriever, instructions)

	listResult := invokeSkillSearch(t, toolSet, json.RawMessage(`{}`))
	if len(listResult.Skills) != 1 || listResult.Skills[0].Name != "available" {
		t.Fatalf("expected unavailable skill filtered from list, got %+v", listResult)
	}

	searchResult := invokeSkillSearch(t, toolSet, json.RawMessage(`{"queries":[{"description":"unavailable"}]}`))
	if len(retriever.instructions) != 1 || retriever.instructions[0].Name != "available" {
		t.Fatalf("expected retriever to receive one filtered catalog, got %+v", retriever.instructions)
	}
	if len(searchResult.Skills) != 1 || searchResult.Skills[0].Name != "available" {
		t.Fatalf("expected unavailable candidate and exact injection blocked, got %+v", searchResult)
	}

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.SkillSearchToolName,
		Input:    json.RawMessage(`{"name":"unavailable"}`),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != agent.FailureCodes.NotFound.String() {
		t.Fatalf("expected unavailable exact lookup to fail closed, got %+v", result)
	}
}

func TestSkillSearchNameModeRejectsCaseInsensitiveNameCollision(t *testing.T) {
	toolSet := canonicalSkillSearchToolSet(&recordingSkillSearchRetriever{}, []agent.SkillInstruction{
		{Name: "mail", Description: "First."},
		{Name: "MAIL", Description: "Second."},
	})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.SkillSearchToolName,
		Input:    json.RawMessage(`{"name":"Mail"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != agent.FailureCodes.NotFound.String() {
		t.Fatalf("expected ambiguous exact name to fail closed, got %+v", result)
	}
}

func TestSkillSearchResultContractRejectsMalformedOutput(t *testing.T) {
	testCases := []json.RawMessage{
		json.RawMessage(`{"mode":"list","skills":null,"totalCount":0,"hasMore":false}`),
		json.RawMessage(`{"mode":"name","skills":[{"name":"mail","description":"Mail","toolReferences":[]}],"totalCount":1,"hasMore":false}`),
		json.RawMessage(`{"mode":"search","skills":[{"name":"mail","description":"Mail","toolReferences":[],"prompt":"private"}],"totalCount":1,"hasMore":false}`),
	}
	for _, document := range testCases {
		t.Run(string(document), func(t *testing.T) {
			handlerToolSet := agent.NewToolSet(nil)
			handlerToolSet.RegisterTool(agent.ToolDefinition{
				Name:        agent.SkillSearchToolName,
				Description: "Search available skills.",
				InputSchema: skillSearchInputSchema,
			}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
				return agent.ToolSuccessData(string(document), document), nil
			})
			toolSet := agent.NewToolSet([]string{agent.SkillSearchToolName})
			if errorValue := toolSet.RegisterProvider(context.Background(), kernelToolProvider{handlerToolSet: handlerToolSet}); errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
				ToolName: agent.SkillSearchToolName,
				Input:    json.RawMessage(`{}`),
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_result_contract" {
				t.Fatalf("expected malformed skill result rejection, got %+v", result)
			}
		})
	}
}

func canonicalSkillSearchToolSet(retriever agent.SkillRetriever, instructions []agent.SkillInstruction) *agent.ToolSet {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(retriever, func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: instructions}
	})
	return toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
}

func invokeSkillSearch(t *testing.T, toolSet *agent.ToolSet, input json.RawMessage) skillSearchToolOutput {
	t.Helper()
	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: agent.SkillSearchToolName,
		Input:    input,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill search success, got %+v", result)
	}
	var output skillSearchToolOutput
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	return output
}

func decodeSkillSearchDocument(t *testing.T, output skillSearchToolOutput) map[string]any {
	t.Helper()
	document, errorValue := json.Marshal(output)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var decoded map[string]any
	if errorValue := json.Unmarshal(document, &decoded); errorValue != nil {
		t.Fatal(errorValue)
	}
	return decoded
}
