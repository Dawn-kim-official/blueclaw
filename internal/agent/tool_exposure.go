package agent

import (
	"encoding/json"
	"strings"
)

type toolExposureGroup struct {
	Name    string
	ToolIDs []string
}

type ToolExposureEvent struct {
	SelectionReason      string   `json:"selectionReason"`
	SelectionSource      string   `json:"selectionSource"`
	ExposedToolIDs       []string `json:"exposedToolIDs"`
	SelectedSkillToolIDs []string `json:"selectedSkillToolIDs,omitempty"`
}

func baseToolNames(toolSet *ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolSet.ListBaseToolNames() {
		if toolName == CapabilityInvokeToolName {
			continue
		}
		if IsKernelToolName(toolName) || toolSet.CanExpose(toolName) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func toolSetForAgentTurnWithExposure(toolSet *ToolSet, instructionBundle InstructionBundle) (*ToolSet, ToolExposureEvent) {
	if toolSet == nil {
		return nil, ToolExposureEvent{}
	}
	baseGroup := toolExposureGroup{Name: "base tools", ToolIDs: baseToolNames(toolSet)}
	selectedSkillGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "selected skill allowed-tools", ToolIDs: selectedSkillToolNames(instructionBundle)})
	exposedToolIDs := stableUniqueToolIDs(append(append([]string{}, baseGroup.ToolIDs...), selectedSkillGroup.ToolIDs...))
	return toolSet.withAllowedToolNamesPreservingBase(exposedToolIDsForFiltering(exposedToolIDs)), ToolExposureEvent{
		SelectionSource:      "selected_skill_allowed_tools",
		SelectionReason:      "Blueclaw exposes base tools and the selected skills' allowed-tools",
		ExposedToolIDs:       append([]string{}, exposedToolIDs...),
		SelectedSkillToolIDs: append([]string{}, selectedSkillGroup.ToolIDs...),
	}
}

func selectedSkillToolNames(instructionBundle InstructionBundle) []string {
	selectedSkillNames := map[string]bool{}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillNames[skillDecision.Name] = true
		}
	}
	toolNames := []string{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillNames[skillInstruction.Name] {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func exposedToolIDsForFiltering(exposedToolIDs []string) []string {
	if len(exposedToolIDs) > 0 {
		return exposedToolIDs
	}
	return []string{"__blueclaw_no_callable_tools__"}
}

func toolSetForAgentTurn(toolSet *ToolSet, instructionBundle InstructionBundle) *ToolSet {
	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle)
	return filteredToolSet
}

func filterGroupTools(toolSet *ToolSet, group toolExposureGroup) toolExposureGroup {
	filteredToolIDs := []string{}
	for _, toolID := range group.ToolIDs {
		trimmedToolID := strings.TrimSpace(toolID)
		if trimmedToolID != "" && trimmedToolID != CapabilityInvokeToolName && toolSet != nil && toolSet.CanExpose(trimmedToolID) {
			filteredToolIDs = appendUniqueStrings(filteredToolIDs, trimmedToolID)
		}
	}
	return toolExposureGroup{Name: group.Name, ToolIDs: filteredToolIDs}
}

func toolIsModelCallable(toolID string) bool {
	return strings.TrimSpace(toolID) != ""
}

func outcomeContractSummary(contract OutcomeContract) string {
	if !OutcomeContractHasRequirements(contract) {
		return ""
	}
	document, errorValue := json.Marshal(contract)
	if errorValue != nil {
		return ""
	}
	return "Outcome contract: " + string(document)
}

func stableUniqueToolIDs(toolIDs []string) []string {
	result := []string{}
	return appendUniqueStrings(result, toolIDs...)
}

func visibleContextMaterialReadToolNames(visibleContext VisibleContext) []string {
	toolNames := []string{}
	for _, material := range allVisibleContextMaterials(visibleContext) {
		if visibleContextMaterialLooksLikeImage(material) {
			toolNames = appendUniqueStrings(toolNames, "image.read")
			continue
		}
		if visibleContextMaterialLooksReadableDocument(material) {
			toolNames = appendUniqueStrings(toolNames, "file.preview")
		}
	}
	return toolNames
}

func allVisibleContextMaterials(visibleContext VisibleContext) []VisibleContextMaterial {
	materials := append([]VisibleContextMaterial{}, visibleContext.CurrentMaterials...)
	materials = append(materials, visibleContext.Materials...)
	for _, message := range visibleContext.Messages {
		materials = append(materials, message.Materials...)
	}
	return materials
}

func visibleContextMaterialLooksLikeImage(material VisibleContextMaterial) bool {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(material.Filename))
	return strings.HasSuffix(filename, ".png") ||
		strings.HasSuffix(filename, ".jpg") ||
		strings.HasSuffix(filename, ".jpeg") ||
		strings.HasSuffix(filename, ".gif") ||
		strings.HasSuffix(filename, ".webp")
}

func visibleContextMaterialLooksReadableDocument(material VisibleContextMaterial) bool {
	if visibleContextMaterialLooksLikeImage(material) {
		return false
	}
	return strings.TrimSpace(material.Filename) != "" ||
		strings.TrimSpace(material.ContentType) != "" ||
		strings.TrimSpace(material.Path) != "" ||
		strings.TrimSpace(material.MaterialID) != ""
}
