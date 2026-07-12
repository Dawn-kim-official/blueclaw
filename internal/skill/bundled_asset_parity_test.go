package skill_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/skill"
)

func TestBundledSkillAssetsMatchLoaderAndRuntimeToolCatalogs(t *testing.T) {
	internKimRootPath := bundledAssetInternKimRootPath(t)
	skillDocumentPaths, errorValue := filepath.Glob(filepath.Join(internKimRootPath, "assets", "blueclaw-workspace", "skills", "*", "SKILL.md"))
	if errorValue != nil {
		t.Fatalf("expected valid bundled skill glob: %v", errorValue)
	}
	if len(skillDocumentPaths) == 0 {
		t.Fatal("expected bundled SKILL.md assets")
	}

	expectedOperationNamesBySkill := bundledSkillWorkflowOperationNames()
	registeredToolNames := authoritativeRuntimeToolNames(t, internKimRootPath)
	skillLoader := skill.SkillLoader{}
	bundledSkillNames := map[string]bool{}
	for _, skillDocumentPath := range skillDocumentPaths {
		skillDirectoryPath := filepath.Dir(skillDocumentPath)
		skillDirectoryName := filepath.Base(skillDirectoryPath)
		bundledSkillNames[skillDirectoryName] = true
		t.Run(skillDirectoryName, func(t *testing.T) {
			skillBundle, loadError := skillLoader.LoadSkillBundle(skillDirectoryPath)
			if loadError != nil {
				t.Fatalf("real skill loader failed to parse %s: %v", skillDocumentPath, loadError)
			}
			if skillBundle.Name != skillDirectoryName {
				t.Fatalf("bundled skill metadata name %q must match directory %q", skillBundle.Name, skillDirectoryName)
			}
			assertExpectedWorkflowOperations(t, skillBundle, expectedOperationNamesBySkill[skillDirectoryName])
			for _, allowedToolName := range skillBundle.AllowedTools {
				if !registeredToolNames[allowedToolName] {
					t.Errorf("bundled skill %q declares allowed tool %q, but no kernel or authoritative runtime catalog registers it", skillDirectoryName, allowedToolName)
				}
			}
		})
	}
	for expectedSkillName := range expectedOperationNamesBySkill {
		if !bundledSkillNames[expectedSkillName] {
			t.Errorf("expected capability-dependent bundled skill asset %q", expectedSkillName)
		}
	}
}

func assertExpectedWorkflowOperations(t *testing.T, skillBundle skill.SkillBundle, expectedOperationNames []string) {
	t.Helper()
	if len(expectedOperationNames) == 0 {
		return
	}
	if len(skillBundle.AllowedTools) == 0 {
		t.Fatalf("bundled skill %q must declare non-empty allowed-tools for workflow operations %v", skillBundle.Name, expectedOperationNames)
	}
	allowedToolNames := stringMembership(skillBundle.AllowedTools)
	for _, expectedOperationName := range expectedOperationNames {
		if !allowedToolNames[expectedOperationName] {
			t.Errorf("bundled skill %q allowed-tools missing workflow operation %q; declared %v", skillBundle.Name, expectedOperationName, skillBundle.AllowedTools)
		}
	}
}

func bundledSkillWorkflowOperationNames() map[string][]string {
	return map[string][]string{
		"calculator":         {"math.calculate"},
		"calendar":           {"calendar.add", "calendar.list", "calendar.update", "calendar.delete"},
		"company-data":       {"company.info.get", "company.info.set", "company.metric.record", "company.metric.list", "company.record.add", "company.record.list", "company.record.update", "company.record.delete", "company.document.register", "company.document.list", "company.document.search", "company.document.update"},
		"create-gws-file":    {"google.docs.create", "google.sheets.create", "google.gmail.send"},
		"direct-message":     {"message.send"},
		"internkim-flow":     {"task.add", "task.list", "task.update", "task.delete"},
		"mail":               {"mail.connection.status", "mail.connection.start", "mail.message.list", "mail.message.search", "mail.message.read", "mail.message.send"},
		"mattermost":         {"message.context", "message.search", "message.send", "message.update", "message.delete", "channel.update", "schedule.cancel"},
		"paperwork":          {"company.info.get", "company.info.set", "company.document.register", "company.document.list", "company.document.search", "company.document.update"},
		"scheduled-task":     {"schedule.create", "schedule.cancel"},
		"skill-management":   {"skill.add", "skill.remove"},
		"web-search":         {"web.search", "web.fetch"},
		"website":            {"site.status", "site.create", "site.preview", "site.repair", "artifact.review", "site.publish"},
		"website-management": {"site.status", "site.history", "site.diff", "site.logs", "site.repair", "site.rollback", "site.unpublish", "site.restore", "site.delete"},
	}
}

func authoritativeRuntimeToolNames(t *testing.T, internKimRootPath string) map[string]bool {
	t.Helper()
	toolNames := stringMembership(agent.KernelToolNames())
	toolCatalog := agentruntime.NewToolCatalogBuilder().BuildToolSet(agentruntime.ToolCatalogRequest{})
	for _, registeredToolName := range toolCatalog.ListRegisteredToolNames() {
		toolNames[registeredToolName] = true
	}
	capabilitySourcePath := filepath.Join(internKimRootPath, "internal", "capabilities", "protocol.go")
	for _, operationName := range descriptorOperationNames(t, capabilitySourcePath) {
		toolNames[operationName] = true
	}
	return toolNames
}

func descriptorOperationNames(t *testing.T, sourcePath string) []string {
	t.Helper()
	parsedFile, errorValue := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if errorValue != nil {
		t.Fatalf("expected authoritative capability descriptor source %s: %v", sourcePath, errorValue)
	}
	operationNames := []string{}
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		keyValue, isKeyValue := node.(*ast.KeyValueExpr)
		if !isKeyValue {
			return true
		}
		key, isIdentifier := keyValue.Key.(*ast.Ident)
		value, isString := keyValue.Value.(*ast.BasicLit)
		if !isIdentifier || key.Name != "Name" || !isString || value.Kind != token.STRING {
			return true
		}
		operationName, unquoteError := strconv.Unquote(value.Value)
		if unquoteError == nil && strings.Contains(operationName, ".") {
			operationNames = append(operationNames, operationName)
		}
		return true
	})
	if len(operationNames) == 0 {
		t.Fatalf("expected operation descriptors in authoritative source %s", sourcePath)
	}
	return operationNames
}

func bundledAssetInternKimRootPath(t *testing.T) string {
	t.Helper()
	_, sourceFilename, _, isAvailable := runtime.Caller(0)
	if !isAvailable {
		t.Fatal("expected runtime caller for bundled asset path")
	}
	internKimRootPath := filepath.Clean(filepath.Join(filepath.Dir(sourceFilename), "..", "..", "..", ".."))
	skillRootPath := filepath.Join(internKimRootPath, "assets", "blueclaw-workspace", "skills")
	if _, errorValue := os.Stat(skillRootPath); errorValue != nil {
		t.Fatalf("expected parent InternKim skill assets at %s: %v", skillRootPath, errorValue)
	}
	return internKimRootPath
}

func stringMembership(values []string) map[string]bool {
	membership := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			membership[trimmedValue] = true
		}
	}
	return membership
}
