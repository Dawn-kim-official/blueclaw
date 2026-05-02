package skill

import (
	"os"
	"path/filepath"
	"strings"
)

type SkillLoader struct{}

func (skillLoader SkillLoader) LoadSkillBundle(directoryPath string) (SkillBundle, error) {
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document, errorValue := os.ReadFile(documentPath)
	if errorValue != nil {
		return SkillBundle{}, errorValue
	}

	metadata, instruction := parseSkillDocument(string(document))
	if strings.TrimSpace(metadata.Name) == "" {
		metadata.Name = filepath.Base(directoryPath)
	}

	return SkillBundle{
		Name:            metadata.Name,
		Description:     metadata.Description,
		Category:        metadata.Category,
		Tags:            metadata.Tags,
		Activation:      metadata.Activation,
		Completion:      metadata.Completion,
		RequiredTools:   metadata.RequiredTools,
		AllowedProfiles: metadata.AllowedProfiles,
		TriggerHints:    metadata.TriggerHints,
		References:      metadata.References,
		Scripts:         metadata.Scripts,
		Assets:          metadata.Assets,
		Instruction:     strings.TrimSpace(instruction),
		DirectoryPath:   directoryPath,
	}, nil
}

type skillMetadata struct {
	Name            string
	Description     string
	Category        string
	Tags            []string
	Activation      SkillActivation
	Completion      SkillCompletion
	RequiredTools   []string
	AllowedProfiles []string
	TriggerHints    []string
	References      []string
	Scripts         []string
	Assets          []string
}

func parseSkillDocument(document string) (skillMetadata, string) {
	trimmedDocument := strings.TrimSpace(document)
	if !strings.HasPrefix(trimmedDocument, "---\n") {
		return skillMetadata{}, document
	}
	remainingDocument := strings.TrimPrefix(trimmedDocument, "---\n")
	frontmatter, instruction, hasFrontmatter := strings.Cut(remainingDocument, "\n---")
	if !hasFrontmatter {
		return skillMetadata{}, document
	}
	return parseSkillFrontmatter(frontmatter), strings.TrimSpace(instruction)
}

func parseSkillFrontmatter(frontmatter string) skillMetadata {
	metadata := skillMetadata{}
	section := ""
	listKey := ""
	for _, line := range strings.Split(frontmatter, "\n") {
		isIndentedLine := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "- ") {
			metadata = appendSkillFrontmatterListValue(metadata, section, listKey, strings.TrimSpace(strings.TrimPrefix(trimmedLine, "- ")))
			continue
		}
		key, value, hasKey := strings.Cut(trimmedLine, ":")
		if !hasKey {
			metadata = appendSkillFrontmatterScalarContinuation(metadata, section, trimmedLine)
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			if (section == "activation" || section == "completion") && isIndentedLine {
				listKey = key
				continue
			}
			section = key
			listKey = key
			continue
		}
		if section == "activation" && isIndentedLine {
			metadata = setSkillActivationValue(metadata, key, value)
			listKey = key
			continue
		}
		if section == "completion" && isIndentedLine {
			metadata = setSkillCompletionValue(metadata, key, value)
			listKey = key
			continue
		}
		metadata = setSkillMetadataValue(metadata, key, value)
		listKey = key
	}
	return metadata
}

func setSkillMetadataValue(metadata skillMetadata, key string, value string) skillMetadata {
	switch key {
	case "name":
		metadata.Name = cleanSkillScalar(value)
	case "description":
		metadata.Description = joinSkillDescription(metadata.Description, cleanSkillScalar(value))
	case "category":
		metadata.Category = cleanSkillScalar(value)
	case "tags":
		metadata.Tags = append(metadata.Tags, parseSkillList(value)...)
	case "requiredTools":
		metadata.RequiredTools = append(metadata.RequiredTools, parseSkillList(value)...)
	case "allowedProfiles":
		metadata.AllowedProfiles = append(metadata.AllowedProfiles, parseSkillList(value)...)
	case "triggerHints":
		metadata.TriggerHints = append(metadata.TriggerHints, parseSkillList(value)...)
	case "references":
		metadata.References = append(metadata.References, parseSkillList(value)...)
	case "scripts":
		metadata.Scripts = append(metadata.Scripts, parseSkillList(value)...)
	case "assets":
		metadata.Assets = append(metadata.Assets, parseSkillList(value)...)
	}
	return metadata
}

func setSkillActivationValue(metadata skillMetadata, key string, value string) skillMetadata {
	values := parseSkillList(value)
	switch key {
	case "keywords":
		metadata.Activation.Keywords = append(metadata.Activation.Keywords, values...)
	case "toolNames":
		metadata.Activation.ToolNames = append(metadata.Activation.ToolNames, values...)
	case "toolPrefixes":
		metadata.Activation.ToolPrefixes = append(metadata.Activation.ToolPrefixes, values...)
	}
	return metadata
}

func setSkillCompletionValue(metadata skillMetadata, key string, value string) skillMetadata {
	values := parseSkillList(value)
	switch key {
	case "requiredEvidenceTools":
		metadata.Completion.RequiredEvidenceTools = append(metadata.Completion.RequiredEvidenceTools, values...)
	}
	return metadata
}

func appendSkillFrontmatterListValue(metadata skillMetadata, section string, listKey string, value string) skillMetadata {
	values := parseSkillList(value)
	if section == "activation" {
		return setSkillActivationValue(metadata, listKey, strings.Join(values, ","))
	}
	if section == "completion" {
		return setSkillCompletionValue(metadata, listKey, strings.Join(values, ","))
	}
	return setSkillMetadataValue(metadata, listKey, strings.Join(values, ","))
}

func appendSkillFrontmatterScalarContinuation(metadata skillMetadata, section string, value string) skillMetadata {
	if section == "description" {
		metadata.Description = joinSkillDescription(metadata.Description, cleanSkillScalar(value))
	}
	return metadata
}

func parseSkillList(value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if strings.HasPrefix(trimmedValue, "[") && strings.HasSuffix(trimmedValue, "]") {
		trimmedValue = strings.TrimSuffix(strings.TrimPrefix(trimmedValue, "["), "]")
	}
	values := []string{}
	for _, part := range strings.Split(trimmedValue, ",") {
		cleanedPart := cleanSkillScalar(part)
		if cleanedPart != "" {
			values = append(values, cleanedPart)
		}
	}
	return values
}

func cleanSkillScalar(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func joinSkillDescription(left string, right string) string {
	if strings.TrimSpace(left) == "" {
		return strings.TrimSpace(right)
	}
	if strings.TrimSpace(right) == "" {
		return strings.TrimSpace(left)
	}
	return strings.TrimSpace(left) + " " + strings.TrimSpace(right)
}
