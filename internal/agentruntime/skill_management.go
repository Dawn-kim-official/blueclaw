package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"blueclaw/internal/agent"
	"blueclaw/internal/skill"
)

const maximumSkillNameLength = 64
const weakDescriptionRuneCount = 40
const longSkillBodyLineCount = 500

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var builtInSkillNames = map[string]bool{
	"agent-browser":                     true,
	"bash":                              true,
	"calendar":                          true,
	"corporate-registration-consulting": true,
	"court-auction-notice-search":       true,
	"create-gws-file":                   true,
	"delivery-tracking":                 true,
	"hwp":                               true,
	"internkim-flow":                    true,
	"iros-registry-automation":          true,
	"k-dart":                            true,
	"korean-character-count":            true,
	"korean-jangbu-for":                 true,
	"korean-law-search":                 true,
	"korean-patent-search":              true,
	"korean-privacy-terms":              true,
	"korean-spell-check":                true,
	"korean-stock-search":               true,
	"lh-notice-search":                  true,
	"naver-blog-research":               true,
	"naver-news-search":                 true,
	"pdf":                               true,
	"real-estate-search":                true,
	"rhwp-edit":                         true,
	"simple-slides":                     true,
	"site-prototype":                    true,
	"skill-management":                  true,
	"zipcode-search":                    true,
}

var standardSkillFrontmatterKeys = map[string]bool{
	"agent":                    true,
	"allowed-tools":            true,
	"argument-hint":            true,
	"arguments":                true,
	"context":                  true,
	"description":              true,
	"disable-model-invocation": true,
	"effort":                   true,
	"hooks":                    true,
	"hiddenFromCircles":        true,
	"model":                    true,
	"license":                  true,
	"metadata":                 true,
	"name":                     true,
	"paths":                    true,
	"shell":                    true,
	"user-invocable":           true,
	"when_to_use":              true,
}

type skillAddInput struct {
	Name      string               `json:"name"`
	Content   string               `json:"content"`
	Resources []skillResourceInput `json:"resources"`
}

type skillResourceInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
}

type skillAddResult struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Status        string   `json:"status"`
	ResourcePaths []string `json:"resourcePaths"`
	Warnings      []string `json:"warnings"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerSkillManagementTools(toolRegistry *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[skillAddInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "skill.add",
			Description: "Create or update a user-managed SKILL.md under /workspace/.agents/skills/<name>.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"content":{"type":"string"},"resources":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"mode":{"type":"integer"}},"required":["path","content"],"additionalProperties":false}}},"required":["name","content"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.addSkillTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[skillRemoveInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "skill.remove",
			Description: "Remove a user-managed skill under /workspace/.agents/skills/<name>.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.removeSkillTool,
		Result:  agent.IdentityToolResult,
	})
}

type skillRemoveInput struct {
	Name string `json:"name"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) addSkillTool(toolContext context.Context, input skillAddInput) (agent.ToolResult, error) {
	skillName := strings.TrimSpace(input.Name)
	if errorValue := validateUserManagedSkillName(skillName); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	skillBundle, warnings, errorValue := validateSkillDocument(skillName, input.Content)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	resourcePaths, errorValue := validateSkillResources(input.Resources)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	warnings = append(warnings, skillResourceWarnings(skillBundle.Instruction, resourcePaths)...)
	skillDirectoryPath := toolCatalogBuilder.userManagedSkillDirectoryPath(skillName)
	status := "updated"
	if _, errorValue := os.Stat(skillDirectoryPath); os.IsNotExist(errorValue) {
		status = "created"
	}
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), normalizedSkillDocument(input.Content), 0600); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	writtenResourcePaths, errorValue := toolCatalogBuilder.writeSkillResources(skillDirectoryPath, input.Resources)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	toolCatalogBuilder.refreshSkills(toolContext)
	return agent.ToolResult{Content: marshalToolResult(skillAddResult{
		Name:          skillName,
		Path:          toolCatalogBuilder.agentWorkspacePath(filepath.Join(skillDirectoryPath, "SKILL.md")),
		Status:        status,
		ResourcePaths: writtenResourcePaths,
		Warnings:      warnings,
	})}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) removeSkillTool(toolContext context.Context, input skillRemoveInput) (agent.ToolResult, error) {
	skillName := strings.TrimSpace(input.Name)
	if errorValue := validateUserManagedSkillName(skillName); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	skillDirectoryPath := toolCatalogBuilder.userManagedSkillDirectoryPath(skillName)
	if _, errorValue := os.Stat(skillDirectoryPath); os.IsNotExist(errorValue) {
		return agent.ToolResult{Content: marshalToolResult(map[string]string{
			"name":   skillName,
			"path":   toolCatalogBuilder.agentWorkspacePath(skillDirectoryPath),
			"status": "missing",
		})}, nil
	}
	if errorValue := os.RemoveAll(skillDirectoryPath); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	toolCatalogBuilder.refreshSkills(toolContext)
	return agent.ToolResult{Content: marshalToolResult(map[string]string{
		"name":   skillName,
		"path":   toolCatalogBuilder.agentWorkspacePath(skillDirectoryPath),
		"status": "removed",
	})}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) userManagedSkillDirectoryPath(skillName string) string {
	return filepath.Join(toolCatalogBuilder.workspaceRootPath, ".agents", "skills", skillName)
}

func (toolCatalogBuilder *ToolCatalogBuilder) refreshSkills(ctx context.Context) {
	if toolCatalogBuilder.skillChangeHandler != nil {
		toolCatalogBuilder.skillChangeHandler(ctx)
	}
}

func validateUserManagedSkillName(skillName string) error {
	if skillName == "" {
		return errors.New("skill name is required")
	}
	if len(skillName) > maximumSkillNameLength || !skillNamePattern.MatchString(skillName) {
		return errors.New("skill name must use lowercase letters, digits, and hyphens only, up to 64 characters")
	}
	if builtInSkillNames[skillName] {
		return errors.New("built-in skills cannot be created, overwritten, or removed")
	}
	return nil
}

func validateSkillDocument(skillName string, content string) (skill.SkillBundle, []string, error) {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return skill.SkillBundle{}, nil, errors.New("skill content is required")
	}
	if errorValue := validateSkillFrontmatter(trimmedContent); errorValue != nil {
		return skill.SkillBundle{}, nil, errorValue
	}
	skillBundle, errorValue := loadSkillDocumentForValidation(skillName, trimmedContent)
	if errorValue != nil {
		return skill.SkillBundle{}, nil, errorValue
	}
	if skillBundle.Name != skillName {
		return skill.SkillBundle{}, nil, errors.New("skill document name must match the requested skill name")
	}
	if strings.TrimSpace(skillBundle.Description) == "" {
		return skill.SkillBundle{}, nil, errors.New("skill document must provide description or a first markdown paragraph")
	}
	if strings.TrimSpace(skillBundle.Instruction) == "" {
		return skill.SkillBundle{}, nil, errors.New("skill document body is required")
	}
	return skillBundle, skillQualityWarnings(skillBundle), nil
}

func validateSkillFrontmatter(content string) error {
	if !strings.HasPrefix(content, "---\n") {
		return errors.New("skill document must start with YAML frontmatter")
	}
	remainingContent := strings.TrimPrefix(content, "---\n")
	frontmatter, _, hasFrontmatter := strings.Cut(remainingContent, "\n---")
	if !hasFrontmatter {
		return errors.New("skill frontmatter is malformed")
	}
	for _, line := range strings.Split(frontmatter, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, _, hasKey := strings.Cut(trimmedLine, ":")
		if !hasKey || !standardSkillFrontmatterKeys[strings.TrimSpace(key)] {
			return errors.New("skill frontmatter contains a non-standard field")
		}
	}
	return nil
}

func loadSkillDocumentForValidation(skillName string, content string) (skill.SkillBundle, error) {
	rootPath, errorValue := os.MkdirTemp("", "blueclaw-skill-validation-*")
	if errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	defer os.RemoveAll(rootPath)
	skillDirectoryPath := filepath.Join(rootPath, skillName)
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(content), 0600); errorValue != nil {
		return skill.SkillBundle{}, errorValue
	}
	return (skill.SkillLoader{}).LoadSkillBundle(skillDirectoryPath)
}

func validateSkillResources(resources []skillResourceInput) ([]string, error) {
	resourcePaths := []string{}
	for _, resource := range resources {
		resourcePath, errorValue := validateSkillResourcePath(resource.Path)
		if errorValue != nil {
			return nil, errorValue
		}
		resourcePaths = append(resourcePaths, resourcePath)
	}
	return resourcePaths, nil
}

func validateSkillResourcePath(path string) (string, error) {
	trimmedPath := filepath.ToSlash(strings.TrimSpace(path))
	if trimmedPath == "" {
		return "", errors.New("skill resource path is required")
	}
	if filepath.IsAbs(trimmedPath) || strings.HasPrefix(trimmedPath, "/") {
		return "", errors.New("skill resource path must be relative")
	}
	cleanPath := filepath.ToSlash(filepath.Clean(trimmedPath))
	if cleanPath == "." || cleanPath == "SKILL.md" || cleanPath == "skill.md" {
		return "", errors.New("skill resource path cannot target SKILL.md")
	}
	if strings.HasPrefix(cleanPath, "../") || cleanPath == ".." {
		return "", errors.New("skill resource path cannot escape the skill directory")
	}
	if hasHiddenPathComponent(cleanPath) {
		return "", errors.New("skill resource path cannot contain hidden path components")
	}
	if !hasAllowedSkillResourcePrefix(cleanPath) {
		return "", errors.New("skill resource path must be under scripts, references, or assets")
	}
	return cleanPath, nil
}

func hasHiddenPathComponent(path string) bool {
	for _, component := range strings.Split(path, "/") {
		if strings.HasPrefix(component, ".") {
			return true
		}
	}
	return false
}

func hasAllowedSkillResourcePrefix(path string) bool {
	return strings.HasPrefix(path, "scripts/") ||
		strings.HasPrefix(path, "references/") ||
		strings.HasPrefix(path, "assets/")
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeSkillResources(skillDirectoryPath string, resources []skillResourceInput) ([]string, error) {
	writtenResourcePaths := []string{}
	for _, resource := range resources {
		resourcePath, errorValue := validateSkillResourcePath(resource.Path)
		if errorValue != nil {
			return nil, errorValue
		}
		fileMode := os.FileMode(0600)
		if resource.Mode != 0 {
			fileMode = os.FileMode(resource.Mode)
		}
		resolvedPath := filepath.Join(skillDirectoryPath, filepath.FromSlash(resourcePath))
		if errorValue := os.MkdirAll(filepath.Dir(resolvedPath), 0700); errorValue != nil {
			return nil, errorValue
		}
		if errorValue := os.WriteFile(resolvedPath, []byte(resource.Content), fileMode); errorValue != nil {
			return nil, errorValue
		}
		writtenResourcePaths = append(writtenResourcePaths, toolCatalogBuilder.agentWorkspacePath(resolvedPath))
	}
	return writtenResourcePaths, nil
}

func skillQualityWarnings(skillBundle skill.SkillBundle) []string {
	warnings := []string{}
	if strings.TrimSpace(skillBundle.WhenToUse) == "" {
		warnings = append(warnings, "when_to_use is recommended so retrieval has explicit trigger context")
	}
	if utf8.RuneCountInString(strings.TrimSpace(skillBundle.Description)) < weakDescriptionRuneCount {
		warnings = append(warnings, "description is short; include what the skill does and when to use it")
	}
	if len(strings.Split(strings.TrimSpace(skillBundle.Instruction), "\n")) > longSkillBodyLineCount {
		warnings = append(warnings, "skill body is long; move detailed material into references")
	}
	return warnings
}

func skillResourceWarnings(instruction string, resourcePaths []string) []string {
	warnings := []string{}
	normalizedInstruction := filepath.ToSlash(instruction)
	resourcePathByPrefix := resourcePathPrefixes(resourcePaths)
	if strings.Contains(normalizedInstruction, "references/") && !resourcePathByPrefix["references"] {
		warnings = append(warnings, "SKILL.md mentions references/ but no reference resources were supplied")
	}
	if strings.Contains(normalizedInstruction, "scripts/") && !resourcePathByPrefix["scripts"] {
		warnings = append(warnings, "SKILL.md mentions scripts/ but no script resources were supplied")
	}
	for _, resourcePath := range resourcePaths {
		if !strings.Contains(normalizedInstruction, resourcePath) {
			warnings = append(warnings, "resource "+resourcePath+" is not mentioned from SKILL.md")
		}
	}
	return warnings
}

func resourcePathPrefixes(resourcePaths []string) map[string]bool {
	prefixes := map[string]bool{}
	for _, resourcePath := range resourcePaths {
		prefix, _, _ := strings.Cut(resourcePath, "/")
		prefixes[prefix] = true
	}
	return prefixes
}

func normalizedSkillDocument(content string) []byte {
	return []byte(strings.TrimSpace(content) + "\n")
}

func isImmutableSkillPath(workspaceRootPath string, path string) bool {
	cleanWorkspaceRootPath, errorValue := filepath.Abs(workspaceRootPath)
	if errorValue != nil {
		return false
	}
	cleanPath, errorValue := filepath.Abs(path)
	if errorValue != nil {
		return false
	}
	relativePath, errorValue := filepath.Rel(cleanWorkspaceRootPath, cleanPath)
	if errorValue != nil {
		return false
	}
	relativePath = filepath.ToSlash(filepath.Clean(relativePath))
	return relativePath == "skills" ||
		strings.HasPrefix(relativePath, "skills/") ||
		relativePath == ".agents/skills/agent-browser" ||
		strings.HasPrefix(relativePath, ".agents/skills/agent-browser/")
}
