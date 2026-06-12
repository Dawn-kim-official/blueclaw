package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

const (
	siteSourceBundleMaximumBytes = 64 * 1024 * 1024
	siteAppStatusToolDescription = "Inspect InternKim site status. Use scope=mine to list all requester-editable sites across conversations. Use checkLive=true to verify live HTTP reachability for published sites."
)

type siteAppBuildToolInput struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
	AppWorkspacePath    string `json:"appWorkspacePath"`
	TimeoutSecond       int    `json:"timeoutSecond"`
}

type siteAppRepairToolInput struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
}

type siteProjectResolutionInput struct {
	SiteID              string
	SourceWorkspacePath string
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerSiteTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[siteAppBuildToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "site.app.build",
			Description: "Build an editable InternKim site project from its canonical appWorkspacePath and return build evidence.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"slug":{"type":"string"},"sourceWorkspacePath":{"type":"string"},"appWorkspacePath":{"type":"string"},"timeoutSecond":{"type":"number"}}}`),
		},
		Handler: func(toolContext context.Context, input siteAppBuildToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.buildSiteAppTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[siteAppRepairToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "site.app.repair",
			Description: "Repair a missing editable InternKim site workspace by recreating the canonical source/app scaffold without changing the published snapshot.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"slug":{"type":"string"},"sourceWorkspacePath":{"type":"string"}}}`),
		},
		Handler: func(toolContext context.Context, input siteAppRepairToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.repairSiteAppTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildSiteAppTool(toolContext context.Context, input siteAppBuildToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	site := siteCreateResult{}
	if shouldResolveSiteStatusForWorkspaceTool(input.SiteID, input.Slug, input.SourceWorkspacePath, input.AppWorkspacePath) {
		resolvedSite, errorValue := toolCatalogBuilder.siteStatusForWorkspaceTool(toolContext, handlerContext.request, input.SiteID, input.Slug)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_status", errorValue.Error()), nil
		}
		site = resolvedSite
	}
	sourceWorkspacePath := firstNonEmptyString(input.SourceWorkspacePath, site.SourceWorkspacePath)
	appWorkspacePath := firstNonEmptyString(input.AppWorkspacePath, site.AppWorkspacePath)
	if sourceWorkspacePath == "" {
		sourceWorkspacePath = sourceWorkspacePathFromSiteAppWorkspacePath(appWorkspacePath)
	}
	if strings.TrimSpace(sourceWorkspacePath) == "" && strings.TrimSpace(appWorkspacePath) == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", "appWorkspacePath could not be resolved; call site.app.status first"), nil
	}
	if strings.HasSuffix(strings.TrimSuffix(filepath.ToSlash(appWorkspacePath), "/"), "/src") {
		canonicalPath := filepath.ToSlash(filepath.Dir(filepath.ToSlash(appWorkspacePath)))
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", "site builds must run from appWorkspacePath "+canonicalPath+", not app/src"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, handlerContext.request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspacePath), siteProjectIDFromPath(appWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", errorValue.Error()), nil
	}
	appWorkspace := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspace.ConcretePath, "app"),
		VirtualPath:  filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app")),
		Kind:         sourceWorkspace.Kind,
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, appWorkspace.ConcretePath) {
		return terminalWorkspaceAccessFailure(appWorkspace.ConcretePath), nil
	}
	appStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(appWorkspace))
	if errorValue != nil || !appStat.IsDirectory {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.NotFound, "site_build_workspace", "appWorkspacePath does not exist; run site.app.repair before building"), nil
	}
	if toolFailure := ensureManagedSiteBuildScript(toolContext, workspaceActor, appWorkspace); toolFailure != nil {
		return *toolFailure, nil
	}
	timeoutSecond := input.TimeoutSecond
	if timeoutSecond <= 0 {
		timeoutSecond = 180
	}
	buildResult, errorValue := toolCatalogBuilder.runTerminalTool(toolContext, security.CommandRequest{
		Command:              "bun scripts/build.ts",
		WorkingDirectoryPath: appWorkspace.VirtualPath,
		TimeoutSecond:        timeoutSecond,
	}, handlerContext)
	if errorValue != nil {
		return buildResult, errorValue
	}
	if buildResult.Failed() {
		return siteBuildCommandFailureResult(buildResult, appWorkspace), nil
	}
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(filepath.Dir(appWorkspace.ConcretePath), ".internkim", "build-quality.json"),
		VirtualPath:  filepath.ToSlash(filepath.Join(filepath.Dir(appWorkspace.VirtualPath), ".internkim", "build-quality.json")),
		Kind:         appWorkspace.Kind,
	}
	if toolFailure := writeSuccessfulSiteBuildQuality(toolContext, workspaceActor, qualityPath); toolFailure != nil {
		return *toolFailure, nil
	}
	result := map[string]any{
		"siteID":              firstNonEmptyString(input.SiteID, site.SiteID),
		"slug":                firstNonEmptyString(input.Slug, site.Slug),
		"sourceWorkspacePath": sourceWorkspace.VirtualPath,
		"appWorkspacePath":    appWorkspace.VirtualPath,
		"distPath":            filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "dist")),
		"qualityPath":         filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, ".internkim", "build-quality.json")),
		"command":             "bun scripts/build.ts",
		"commandResult":       json.RawMessage(buildResult.ContentText()),
	}
	for key, value := range siteBuildQualityPayload(toolContext, workspaceActor, sourceWorkspace, appWorkspace) {
		result[key] = value
	}
	if deliveryBlocked, _ := result["deliveryBlocked"].(bool); deliveryBlocked {
		return siteDeliveryBlockedBuildResult(result), nil
	}
	return agent.ToolSuccess(marshalToolResult(result)), nil
}

func siteBuildCommandFailureResult(buildResult agent.ToolResult, appWorkspace workspacepath.Path) agent.ToolResult {
	payload := siteBuildFailurePayload(buildResult, appWorkspace)
	buildResult.Output.Data = json.RawMessage(marshalToolResult(payload))
	if siteBuildFailureLooksSourceFixable(buildResult.ContentText()) {
		buildResult.Failure = &agent.ToolFailure{
			Kind:                  agent.FailureInvalidInput,
			Code:                  agent.FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_source",
			UserSafeSummary:       siteBuildSourceFailureSummary(buildResult.ContentText()),
			Retryable:             true,
			SafeRetry:             true,
			FailureClass:          "fixable_artifact_quality",
			RetryPolicy:           "after_precondition",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []agent.RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
				Reason:    "Inspect the source file named in the build error, fix the syntax or content, then run site.app.build again.",
			}},
			AffectedResources: siteBuildFailureAffectedResources(buildResult.ContentText(), appWorkspace),
		}
	}
	return buildResult
}

func siteBuildFailurePayload(buildResult agent.ToolResult, appWorkspace workspacepath.Path) map[string]any {
	content := buildResult.ContentText()
	sourceWorkspacePath := filepath.Dir(appWorkspace.ConcretePath)
	return map[string]any{
		"target":                  appWorkspace.VirtualPath,
		"stderrTail":              terminalFailureTail(content),
		"likelyCause":             siteBuildLikelyCause(content),
		"suggestedSourceFiles":    siteBuildFailureSuggestedSourceFiles(content, appWorkspace),
		"canUseExistingFreshDist": siteBuildStatus(sourceWorkspacePath) == "fresh",
		"buildStatus":             siteBuildStatus(sourceWorkspacePath),
		"command":                 "bun scripts/build.ts",
		"commandResult":           content,
	}
}

func terminalFailureTail(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) <= 24 {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(strings.Join(lines[len(lines)-24:], "\n"))
}

func siteBuildLikelyCause(content string) string {
	lowerContent := strings.ToLower(content)
	switch {
	case strings.Contains(lowerContent, "command not found") && strings.Contains(lowerContent, "bun"):
		return "managed runtime PATH does not include bun"
	case strings.Contains(lowerContent, "permission denied") || strings.Contains(lowerContent, "eacces"):
		return "workspace permission problem"
	case siteBuildFailureLooksSourceFixable(content):
		return "editable source compile error"
	case strings.Contains(lowerContent, "cannot find module") || strings.Contains(lowerContent, "could not resolve"):
		return "dependency or import resolution error"
	default:
		return "site build command failed"
	}
}

func siteBuildFailureSuggestedSourceFiles(content string, appWorkspace workspacepath.Path) []string {
	files := []string{}
	for _, resource := range siteBuildFailureAffectedResources(content, appWorkspace) {
		files = append(files, resource.Path)
	}
	if len(files) > 0 {
		return files
	}
	return []string{
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "App.tsx")),
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "index.css")),
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "prototype-data.ts")),
	}
}

func siteBuildFailureLooksSourceFixable(content string) bool {
	lowerContent := strings.ToLower(content)
	return strings.Contains(lowerContent, "syntax error") ||
		strings.Contains(lowerContent, "transform failed") ||
		strings.Contains(lowerContent, "vite:esbuild") ||
		strings.Contains(lowerContent, "/src/")
}

func siteBuildSourceFailureSummary(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(trimmedLine), "syntax error") {
			return trimmedLine
		}
	}
	return "site source failed to compile; inspect and fix the affected source file."
}

func siteBuildFailureAffectedResources(content string, appWorkspace workspacepath.Path) []agent.AffectedResource {
	resources := []agent.AffectedResource{}
	for _, resourcePath := range siteBuildFailureSourcePaths(content, appWorkspace) {
		resources = append(resources, agent.AffectedResource{
			Path:   resourcePath,
			Role:   "source",
			Reason: "Build reported a source compile error here.",
		})
	}
	return resources
}

func siteBuildFailureSourcePaths(content string, appWorkspace workspacepath.Path) []string {
	paths := []string{}
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "/src/") {
			continue
		}
		for _, field := range strings.Fields(line) {
			cleanField := strings.Trim(field, "\"'`,:;()[]{}")
			sourceIndex := strings.Index(cleanField, "/src/")
			if sourceIndex < 0 {
				continue
			}
			sourcePath := siteBuildFailureSourcePathFromField(cleanField[sourceIndex+1:])
			paths = append(paths, filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, sourcePath)))
		}
	}
	return stableUniqueStrings(paths)
}

func siteBuildFailureSourcePathFromField(field string) string {
	sourcePath := strings.Trim(field, "\"'`,:;()[]{}")
	for _, extension := range []string{".tsx", ".ts", ".jsx", ".js", ".css"} {
		if index := strings.Index(sourcePath, extension); index >= 0 {
			return sourcePath[:index+len(extension)]
		}
	}
	return sourcePath
}

func siteDeliveryBlockedBuildResult(result map[string]any) agent.ToolResult {
	return agent.ToolResult{
		Output: agent.ToolOutput{Content: marshalToolResult(result)},
		Failure: &agent.ToolFailure{
			Kind:                  agent.FailureInvalidInput,
			Code:                  agent.FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_delivery",
			UserSafeSummary:       siteDeliveryBlockedSummary(result),
			Retryable:             true,
			SafeRetry:             true,
			FailureClass:          "fixable_artifact_quality",
			RetryPolicy:           "after_precondition",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []agent.RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
				Reason:    "Replace starter scaffold content in editableTargets, then run site.app.build again.",
			}},
			AffectedResources: siteDeliveryBlockedAffectedResources(result),
		},
	}
}

func siteDeliveryBlockedSummary(result map[string]any) string {
	if blockers, isSlice := result["deliveryBlockers"].([]string); isSlice && len(blockers) > 0 {
		return "site build produced a non-deliverable starter scaffold: " + strings.Join(blockers, "; ")
	}
	if blockers, isSlice := result["deliveryBlockers"].([]any); isSlice && len(blockers) > 0 {
		values := []string{}
		for _, blocker := range blockers {
			if text, isString := blocker.(string); isString && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		if len(values) > 0 {
			return "site build produced a non-deliverable starter scaffold: " + strings.Join(values, "; ")
		}
	}
	return "site build produced a non-deliverable starter scaffold; replace starter content before publishing."
}

func siteDeliveryBlockedAffectedResources(result map[string]any) []agent.AffectedResource {
	targets, isSlice := result["editableTargets"].([]string)
	if !isSlice {
		if values, ok := result["editableTargets"].([]any); ok {
			for _, value := range values {
				if text, isString := value.(string); isString {
					targets = append(targets, text)
				}
			}
		}
	}
	resources := []agent.AffectedResource{}
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		resources = append(resources, agent.AffectedResource{
			Path:   strings.TrimSpace(target),
			Role:   "source",
			Reason: "Starter scaffold content must be replaced before delivery.",
		})
	}
	return resources
}

func ensureManagedSiteBuildScript(ctx context.Context, workspaceActor security.WorkspaceActor, appWorkspace workspacepath.Path) *agent.ToolResult {
	buildScriptContent := siteManagedBuildScriptContent()
	if len(buildScriptContent) == 0 {
		result := agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "site_build_scaffold", "managed site build script is unavailable")
		return &result
	}
	buildScriptPath := workspacepath.Path{
		ConcretePath: filepath.Join(appWorkspace.ConcretePath, "scripts", "build.ts"),
		VirtualPath:  filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "scripts", "build.ts")),
		Kind:         appWorkspace.Kind,
	}
	if errorValue := workspaceActor.WriteFile(ctx, buildScriptPath, buildScriptContent, 0660); errorValue != nil {
		result := actorToolFailure("write_file", "site_build_scaffold", buildScriptPath.VirtualPath, errorValue)
		return &result
	}
	return nil
}

func siteBuildQualityPayload(ctx context.Context, workspaceActor security.WorkspaceActor, sourceWorkspace workspacepath.Path, appWorkspace workspacepath.Path) map[string]any {
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspace.ConcretePath, ".internkim", "build-quality.json"),
		VirtualPath:  filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, ".internkim", "build-quality.json")),
		Kind:         sourceWorkspace.Kind,
	}
	payload := map[string]any{
		"qualityStatus":      "missing_report",
		"qualityIssues":      []any{},
		"qualityIssueCount":  0,
		"blockingIssueCount": 0,
		"editableTargets":    []string{},
	}
	qualityDocument, errorValue := workspaceActor.ReadFile(ctx, qualityPath, 1024*1024)
	if errorValue != nil || !json.Valid(qualityDocument) {
		return payload
	}
	var quality map[string]any
	if errorValue := json.Unmarshal(qualityDocument, &quality); errorValue != nil {
		payload["qualityStatus"] = "invalid_report"
		return payload
	}
	issues, _ := quality["issues"].([]any)
	blockingIssueCount, _ := siteBlockingIssueCount(quality)
	payload["qualityIssues"] = issues
	payload["qualityIssueCount"] = len(issues)
	payload["blockingIssueCount"] = blockingIssueCount
	payload["editableTargets"] = siteQualityEditableTargets(quality, appWorkspace)
	deliveryBlockers := siteDeliveryBlockerSummaries(quality)
	if len(deliveryBlockers) > 0 {
		payload["qualityStatus"] = "delivery_blocked"
		payload["deliveryBlocked"] = true
		payload["deliveryBlockers"] = deliveryBlockers
		payload["recommendedNextActions"] = []string{
			"Do not publish while deliveryBlocked is true.",
			"Edit the listed editableTargets to replace starter scaffold content.",
			"Run site.app.build again after source changes.",
		}
		return payload
	}
	if len(issues) > 0 {
		payload["qualityStatus"] = "needs_improvement"
		payload["recommendedNextActions"] = []string{
			"Inspect qualityIssues and editableTargets.",
			"Revise the affected source files when improvement budget remains.",
			"Publish with the report if build output is acceptable and improvement budget is exhausted.",
		}
		return payload
	}
	payload["qualityStatus"] = "passed"
	return payload
}

func siteDeliveryBlockerSummaries(quality map[string]any) []string {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	summaries := []string{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		if !siteQualityIssueBlocksDelivery(issueDocument) {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		text := firstNonEmptyString(strings.TrimSpace(message), strings.TrimSpace(suggestedFix), "Starter scaffold remains.")
		summaries = append(summaries, firstNonEmptyString(strings.TrimSpace(target), "site")+": "+text)
	}
	return summaries
}

func siteQualityIssueBlocksDelivery(issue map[string]any) bool {
	category, _ := issue["category"].(string)
	if strings.TrimSpace(category) == "templateSmell" {
		return true
	}
	return siteQualityIssueContainsStarterMarker(issue)
}

func siteQualityIssueContainsStarterMarker(issue map[string]any) bool {
	for _, value := range issue {
		text, isString := value.(string)
		if !isString {
			continue
		}
		if siteTextContainsStarterMarker(text) {
			return true
		}
	}
	return false
}

func siteTextContainsStarterMarker(value string) bool {
	for _, marker := range []string{
		"INTERNKIM_SITE_STARTER_REPLACE_ME",
		"Replace this starter",
		"Beautiful default scaffold",
		"InternKim React prototype",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func siteQualityAffectedResources(quality map[string]any, appWorkspace workspacepath.Path) []agent.AffectedResource {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	resources := []agent.AffectedResource{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		if strings.TrimSpace(target) == "" {
			continue
		}
		reason := strings.TrimSpace(message)
		if strings.TrimSpace(suggestedFix) != "" {
			reason = strings.TrimSpace(reason + " " + strings.TrimSpace(suggestedFix))
		}
		resources = append(resources, agent.AffectedResource{
			Path:   siteQualityEditableTargetPath(appWorkspace, target),
			Role:   "source",
			Reason: reason,
		})
	}
	return resources
}

func siteQualityIssueSummaries(quality map[string]any) []string {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	summaries := []string{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		category, _ := issueDocument["category"].(string)
		text := firstNonEmptyString(strings.TrimSpace(message), strings.TrimSpace(suggestedFix), strings.TrimSpace(category))
		if text == "" {
			continue
		}
		summaries = append(summaries, firstNonEmptyString(strings.TrimSpace(target), "site")+": "+text)
		if len(summaries) >= 3 {
			return summaries
		}
	}
	return summaries
}

func siteQualityEditableTargets(quality map[string]any, appWorkspace workspacepath.Path) []string {
	resources := siteQualityAffectedResources(quality, appWorkspace)
	targets := []string{}
	for _, resource := range resources {
		if strings.TrimSpace(resource.Path) != "" {
			targets = append(targets, strings.TrimSpace(resource.Path))
		}
	}
	return stableUniqueStrings(targets)
}

func siteQualityEditableTargetPath(appWorkspace workspacepath.Path, target string) string {
	cleanTarget := filepath.ToSlash(filepath.Clean(strings.TrimSpace(target)))
	cleanTarget = strings.TrimPrefix(cleanTarget, "/")
	if cleanTarget == "." || cleanTarget == "" {
		return appWorkspace.VirtualPath
	}
	if strings.HasPrefix(cleanTarget, "app/") {
		return filepath.ToSlash(filepath.Join(filepath.Dir(appWorkspace.VirtualPath), cleanTarget))
	}
	return filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, cleanTarget))
}

func siteBlockingIssueCount(quality map[string]any) (int, bool) {
	value, exists := quality["blockingIssueCount"]
	if !exists {
		return 0, false
	}
	switch typedValue := value.(type) {
	case float64:
		return int(typedValue), true
	case int:
		return typedValue, true
	default:
		return 0, false
	}
}

func writeSuccessfulSiteBuildQuality(ctx context.Context, workspaceActor security.WorkspaceActor, qualityPath workspacepath.Path) *agent.ToolResult {
	quality := map[string]any{
		"generatedAt":         time.Now().UTC().Format(time.RFC3339),
		"generatedBy":         "site.app.build",
		"blockingIssueCount":  0,
		"issues":              []any{},
		"postBuildNormalized": true,
	}
	document, errorValue := workspaceActor.ReadFile(ctx, qualityPath, 1024*1024)
	if errorValue == nil {
		var existing map[string]any
		if json.Unmarshal(document, &existing) == nil {
			quality = existing
			quality["generatedAt"] = time.Now().UTC().Format(time.RFC3339)
			if generatedBy, _ := quality["generatedBy"].(string); strings.TrimSpace(generatedBy) != "" {
				quality["generatedBy"] = strings.TrimSpace(generatedBy)
			} else {
				quality["generatedBy"] = "site.app.build"
			}
			quality["postBuildNormalized"] = true
			if _, exists := quality["blockingIssueCount"]; !exists {
				quality["blockingIssueCount"] = 0
			}
			if _, exists := quality["issues"]; !exists {
				quality["issues"] = []any{}
			}
		}
	}
	qualityDocument, errorValue := json.MarshalIndent(quality, "", "  ")
	if errorValue != nil {
		return toolResultPointer(agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "site_build_quality", errorValue.Error()))
	}
	if errorValue := workspaceActor.MkdirAll(ctx, qualityPath.Parent(), 02770); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_build_quality", qualityPath.VirtualPath, errorValue)
		return &toolFailure
	}
	if errorValue := workspaceActor.WriteFile(ctx, qualityPath, append(qualityDocument, '\n'), 0660); errorValue != nil {
		toolFailure := actorToolFailure("write_file", "site_build_quality", qualityPath.VirtualPath, errorValue)
		return &toolFailure
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) repairSiteAppTool(toolContext context.Context, input siteAppRepairToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	site := siteCreateResult{}
	if shouldResolveSiteStatusForWorkspaceTool(input.SiteID, input.Slug, input.SourceWorkspacePath, "") {
		resolvedSite, errorValue := toolCatalogBuilder.siteStatusForWorkspaceTool(toolContext, handlerContext.request, input.SiteID, input.Slug)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_status", errorValue.Error()), nil
		}
		site = resolvedSite
	}
	sourceWorkspacePath := firstNonEmptyString(input.SourceWorkspacePath, site.SourceWorkspacePath)
	if strings.TrimSpace(sourceWorkspacePath) == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_workspace", "sourceWorkspacePath could not be resolved; call site.app.status first"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, handlerContext.request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_workspace", errorValue.Error()), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, sourceWorkspace.ConcretePath) {
		return terminalWorkspaceAccessFailure(sourceWorkspace.ConcretePath), nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(sourceWorkspace), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "site_repair_workspace", sourceWorkspace.VirtualPath, errorValue), nil
	}
	site.SourceWorkspacePath = sourceWorkspace.VirtualPath
	site.WorkspacePath = canonicalSiteProjectWorkspacePath(firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspace.VirtualPath)))
	site.AppWorkspacePath = filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app"))
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return *toolFailure, nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"siteID":              site.SiteID,
		"slug":                site.Slug,
		"workspacePath":       site.WorkspacePath,
		"sourceWorkspacePath": site.SourceWorkspacePath,
		"draftPath":           site.SourceWorkspacePath,
		"appWorkspacePath":    site.AppWorkspacePath,
		"workspaceHealth":     "ready",
		"publishedUnchanged":  true,
	})), nil
}

func shouldResolveSiteStatusForWorkspaceTool(siteID string, slug string, sourceWorkspacePath string, appWorkspacePath string) bool {
	return strings.TrimSpace(siteID) != "" ||
		strings.TrimSpace(slug) != "" ||
		(strings.TrimSpace(sourceWorkspacePath) == "" && strings.TrimSpace(appWorkspacePath) == "")
}

func (toolCatalogBuilder *ToolCatalogBuilder) siteStatusForWorkspaceTool(toolContext context.Context, request ToolCatalogRequest, siteID string, slug string) (siteCreateResult, error) {
	if toolCatalogBuilder.capabilityClient.Endpoint == "" {
		return siteCreateResult{}, errors.New("site.app.status capability is unavailable")
	}
	input := map[string]string{}
	if strings.TrimSpace(siteID) != "" {
		input["siteID"] = strings.TrimSpace(siteID)
	}
	if strings.TrimSpace(slug) != "" {
		input["slug"] = strings.TrimSpace(slug)
	}
	inputDocument, _ := json.Marshal(input)
	var response struct {
		Result  json.RawMessage `json:"result"`
		IsError bool            `json:"isError"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
	}
	errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/site.app.status/invoke", capabilityToolRequest(toolContext, "site.app.status", request, inputDocument), &response)
	if errorValue != nil {
		return siteCreateResult{}, errorValue
	}
	if response.IsError || response.Status == "error" || response.Status == "denied" {
		return siteCreateResult{}, errors.New(firstNonEmptyString(response.Message, string(response.Result)))
	}
	if len(bytes.TrimSpace(response.Result)) == 0 {
		return siteCreateResult{}, nil
	}
	return decodeSiteCreateResult(response.Result)
}

func siteToolNeedsSourceBundle(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "site.app.publish", "site.app.preview":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) enrichSitePublishInput(toolContext context.Context, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, *agent.ToolResult, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, nil, errorValue
		}
	}
	sourceWorkspacePath := siteSourceWorkspacePath(inputDocument)
	if sourceWorkspacePath == "" {
		sourceWorkspacePath = defaultSiteSourceWorkspacePath(inputDocument)
	}
	if sourceWorkspacePath == "" {
		return toolInput, nil, nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return nil, actorFailure, nil
	}
	resolvedSourcePath, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(siteIDFromInputDocument(inputDocument), siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return nil, nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedSourcePath.ConcretePath) {
		return nil, nil, errors.New("current account cannot publish this site workspace path")
	}
	sourceBundle, errorValue := workspaceActor.BundleDirectory(toolContext, workspacepath.Directory(resolvedSourcePath), security.WorkspaceActorBundleOptions{
		Format:       "tar.gz",
		MaxBytes:     siteSourceBundleMaximumBytes,
		ExcludeNames: siteSourceBundleExcludeNames(),
	})
	if errorValue != nil {
		toolFailure := actorToolFailure("bundle_directory", "site_publish", resolvedSourcePath.VirtualPath, errorValue)
		return nil, &toolFailure, nil
	}
	inputDocument["sourceWorkspacePath"] = resolvedSourcePath.VirtualPath
	inputDocument["sourceBundleBase64"] = sourceBundle.ContentBase64
	inputDocument["sourceBundleFormat"] = sourceBundle.Format
	document, errorValue := json.Marshal(inputDocument)
	return document, nil, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeSiteCreateResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	site, errorValue := decodeSiteCreateResult(*result)
	if errorValue != nil {
		return nil, errorValue
	}
	if strings.TrimSpace(site.SourceWorkspacePath) == "" {
		site.SourceWorkspacePath = defaultSiteSourceWorkspacePath(map[string]any{"siteID": site.SiteID})
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              site.SiteID,
		SourceWorkspacePath: site.SourceWorkspacePath,
	})
	if errorValue != nil {
		return nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, sourceWorkspace.ConcretePath) {
		return toolResultPointer(agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "site_source_workspace", "current account cannot write this site workspace path")), nil
	}
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return toolFailure, nil
	}
	site.SourceWorkspacePath = sourceWorkspace.VirtualPath
	site.WorkspacePath = sourceWorkspace.VirtualPath
	site.AppWorkspacePath = filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app"))
	site.UIPrimitiveImports = siteUIPrimitiveImports()
	site.SourceGuidance = siteSourceGuidance()
	document, errorValue := json.Marshal(site)
	if errorValue != nil {
		return nil, errorValue
	}
	*result = document
	return nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) annotateSiteStatusResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	document := map[string]any{}
	if len(bytes.TrimSpace(*result)) == 0 {
		return nil, nil
	}
	if errorValue := json.Unmarshal(*result, &document); errorValue != nil {
		return nil, errorValue
	}
	if isList, annotatedDocument, errorValue := toolCatalogBuilder.annotateSiteStatusListResult(toolContext, request, document); isList || errorValue != nil {
		if errorValue == nil {
			*result = annotatedDocument
		}
		return nil, errorValue
	}
	toolCatalogBuilder.annotateSiteStatusRecord(toolContext, request, document, true)
	annotatedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return nil, errorValue
	}
	*result = annotatedDocument
	return nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) annotateSiteStatusListResult(toolContext context.Context, request ToolCatalogRequest, document map[string]any) (bool, json.RawMessage, error) {
	rawSites, isList := document["sites"].([]any)
	if !isList {
		return false, nil, nil
	}
	for _, rawSite := range rawSites {
		site, isSite := rawSite.(map[string]any)
		if !isSite {
			continue
		}
		toolCatalogBuilder.annotateSiteStatusRecord(toolContext, request, site, false)
	}
	annotatedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return true, nil, errorValue
	}
	return true, annotatedDocument, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) annotateSiteStatusRecord(toolContext context.Context, request ToolCatalogRequest, document map[string]any, includeWorkspaceDetails bool) {
	if siteStatus, _ := document["status"].(string); strings.TrimSpace(siteStatus) != "published" {
		delete(document, "publishedURL")
	}
	sourceWorkspacePath, _ := document["sourceWorkspacePath"].(string)
	if strings.TrimSpace(sourceWorkspacePath) == "" {
		sourceWorkspacePath = defaultSiteSourceWorkspacePath(document)
	}
	if strings.TrimSpace(sourceWorkspacePath) == "" {
		return
	}
	siteID, _ := document["siteID"].(string)
	health := toolCatalogBuilder.siteWorkspaceHealth(toolContext, request, siteID, sourceWorkspacePath)
	if includeWorkspaceDetails {
		if resolvedSourceWorkspacePath, _ := health["sourceWorkspacePath"].(string); strings.TrimSpace(resolvedSourceWorkspacePath) != "" {
			document["sourceWorkspacePath"] = resolvedSourceWorkspacePath
			document["draftPath"] = resolvedSourceWorkspacePath
			if siteID := firstNonEmptyString(siteID, siteProjectIDFromPath(resolvedSourceWorkspacePath)); siteID != "" {
				document["workspacePath"] = filepath.ToSlash(filepath.Join("home", "sites", siteID))
			}
		}
		if resolvedAppWorkspacePath, _ := health["appWorkspacePath"].(string); strings.TrimSpace(resolvedAppWorkspacePath) != "" {
			document["appWorkspacePath"] = resolvedAppWorkspacePath
		}
		document["workspaceHealthDetails"] = health
	}
	document["workspaceHealth"] = health["status"]
}

func (toolCatalogBuilder *ToolCatalogBuilder) siteWorkspaceHealth(toolContext context.Context, request ToolCatalogRequest, siteID string, sourceWorkspacePath string) map[string]any {
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return map[string]any{"status": "permission_problem", "path": sourceWorkspacePath, "suggestedNextTool": "site.app.repair"}
	}
	resolvedSourcePath, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(siteID, siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return map[string]any{"status": "unknown", "reason": errorValue.Error(), "suggestedNextTool": "site.app.status"}
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, resolvedSourcePath.ConcretePath) {
		return map[string]any{"status": "permission_problem", "path": resolvedSourcePath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	sourceStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(resolvedSourcePath))
	if errorValue != nil || !sourceStat.IsDirectory {
		return map[string]any{"status": "missing", "path": resolvedSourcePath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	appPath := workspacepath.Path{
		ConcretePath: filepath.Join(resolvedSourcePath.ConcretePath, "app"),
		VirtualPath:  filepath.ToSlash(filepath.Join(resolvedSourcePath.VirtualPath, "app")),
		Kind:         resolvedSourcePath.Kind,
	}
	appStat, errorValue := workspaceActor.Stat(toolContext, appPath)
	if errorValue != nil || !appStat.IsDirectory {
		return map[string]any{"status": "missing", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	buildStatus := siteBuildStatus(resolvedSourcePath.ConcretePath)
	if buildStatus != "fresh" {
		return map[string]any{"status": "stale_build", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "buildStatus": buildStatus, "suggestedNextTool": "site.app.build"}
	}
	return map[string]any{"status": "ready", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "buildStatus": buildStatus, "suggestedNextTool": ""}
}

func siteBuildStatus(sourceWorkspacePath string) string {
	distPath := filepath.Join(sourceWorkspacePath, "app", "dist")
	qualityPath := filepath.Join(sourceWorkspacePath, ".internkim", "build-quality.json")
	if !isLocalDirectory(distPath) {
		return "missing_dist"
	}
	if !isLocalRegularFile(filepath.Join(distPath, "index.html")) {
		return "missing_index"
	}
	if !isLocalRegularFile(qualityPath) {
		return "missing_quality"
	}
	latestSourceModTime := latestLocalModTime(filepath.Join(sourceWorkspacePath, "app", "src"))
	qualityInformation, errorValue := os.Stat(qualityPath)
	if errorValue != nil {
		return "missing_quality"
	}
	indexInformation, errorValue := os.Stat(filepath.Join(distPath, "index.html"))
	if errorValue != nil {
		return "missing_index"
	}
	if latestSourceModTime.After(qualityInformation.ModTime()) || latestSourceModTime.After(indexInformation.ModTime()) {
		return "stale"
	}
	return "fresh"
}

func latestLocalModTime(path string) time.Time {
	latestModTime := time.Time{}
	_ = filepath.Walk(path, func(currentPath string, information os.FileInfo, walkError error) error {
		if walkError != nil || information == nil || information.IsDir() {
			return nil
		}
		if information.ModTime().After(latestModTime) {
			latestModTime = information.ModTime()
		}
		_ = currentPath
		return nil
	})
	return latestModTime
}

func isLocalDirectory(path string) bool {
	information, errorValue := os.Stat(path)
	return errorValue == nil && information.IsDir()
}

func isLocalRegularFile(path string) bool {
	information, errorValue := os.Stat(path)
	return errorValue == nil && information.Mode().IsRegular()
}

type siteCreateResult struct {
	SiteID              string           `json:"siteID"`
	Slug                string           `json:"slug"`
	Title               string           `json:"title"`
	Description         string           `json:"description"`
	Idea                string           `json:"idea"`
	OriginalPrompt      string           `json:"originalPrompt"`
	Purpose             string           `json:"purpose"`
	Audience            string           `json:"audience"`
	Archetype           string           `json:"archetype"`
	DomainKeywords      []string         `json:"domainKeywords"`
	CreatedBy           map[string]any   `json:"createdBy"`
	OwnerIdentity       map[string]any   `json:"ownerIdentity"`
	Collaborators       []map[string]any `json:"collaborators"`
	PublishedURL        string           `json:"publishedURL"`
	WorkspacePath       string           `json:"workspacePath"`
	SourceWorkspacePath string           `json:"sourceWorkspacePath"`
	AppWorkspacePath    string           `json:"appWorkspacePath"`
	UIPrimitiveImports  []string         `json:"uiPrimitiveImports,omitempty"`
	SourceGuidance      string           `json:"sourceGuidance,omitempty"`
	Status              string           `json:"status"`
}

func decodeSiteCreateResult(document json.RawMessage) (siteCreateResult, error) {
	if len(bytes.TrimSpace(document)) == 0 {
		return siteCreateResult{}, errors.New("site.app.create returned no result")
	}
	var site siteCreateResult
	if errorValue := json.Unmarshal(document, &site); errorValue != nil {
		return siteCreateResult{}, errorValue
	}
	if strings.TrimSpace(site.SiteID) == "" {
		return siteCreateResult{}, errors.New("site.app.create result is missing siteID")
	}
	return site, nil
}

func writeSiteStarterFiles(ctx context.Context, workspaceActor security.WorkspaceActor, sourceWorkspace workspacepath.Directory, site siteCreateResult) *agent.ToolResult {
	if errorValue := workspaceActor.MkdirAll(ctx, sourceWorkspace, 02770); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_create", sourceWorkspace.VirtualPath, errorValue)
		return &toolFailure
	}
	for _, siteFile := range siteStarterFiles(site) {
		path := workspacePathForSiteStarterFile(sourceWorkspace, siteFile.Path)
		if errorValue := workspaceActor.MkdirAll(ctx, path.Parent(), 02770); errorValue != nil {
			toolFailure := actorToolFailure("mkdir_all", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
		if errorValue := workspaceActor.WriteFile(ctx, path, []byte(siteFile.Content), 0660); errorValue != nil {
			toolFailure := actorToolFailure("write_file", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
	}
	return nil
}

type siteStarterFile struct {
	Path    string
	Content string
}

func workspacePathForSiteStarterFile(sourceWorkspace workspacepath.Directory, relativePath string) workspacepath.Path {
	cleanPath := filepath.Clean(relativePath)
	return workspacepath.Path{
		ConcretePath:      filepath.Join(sourceWorkspace.ConcretePath, cleanPath),
		VirtualPath:       filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, cleanPath)),
		Kind:              sourceWorkspace.Kind,
		IsDurableArtifact: sourceWorkspace.IsDurableArtifact,
	}
}

func siteStarterFiles(site siteCreateResult) []siteStarterFile {
	files := []siteStarterFile{
		{Path: ".internkim/site.json", Content: siteWorkspaceMetadata(site)},
		{Path: ".internkim/idea.md", Content: siteIdeaMarkdown(site)},
		{Path: "DESIGN.md", Content: siteDesignDocument(site)},
	}
	files = append(files, siteAppScaffoldFiles(site)...)
	files = append(files,
		siteStarterFile{Path: "pocketbase/pb_migrations/.gitkeep", Content: ""},
	)
	return files
}

func siteWorkspaceMetadata(site siteCreateResult) string {
	document, errorValue := json.MarshalIndent(map[string]any{
		"siteID":         site.SiteID,
		"slug":           site.Slug,
		"title":          site.Title,
		"publishedURL":   site.PublishedURL,
		"description":    firstNonEmptyString(site.Description, "Editable website prototype project."),
		"idea":           firstNonEmptyString(site.Idea, site.OriginalPrompt, "Original site idea should be refined before publish."),
		"originalPrompt": site.OriginalPrompt,
		"purpose":        firstNonEmptyString(site.Purpose, "prototype for idea validation"),
		"audience":       site.Audience,
		"archetype":      firstNonEmptyString(site.Archetype, "landing"),
		"domainKeywords": site.DomainKeywords,
		"createdBy":      site.CreatedBy,
		"owner":          site.OwnerIdentity,
		"collaborators":  site.Collaborators,
		"stack":          "React + Vite + TypeScript + Tailwind + shadcn/ui scaffold with Stitch DESIGN.md",
		"uiPrimitives":   siteUIPrimitiveImports(),
		"designDefault":  "starter scaffold only; customize through a black-on-white DESIGN.md before publish",
		"sourceContract": "editable source is owned by the requester actor",
	}, "", "  ")
	if errorValue != nil {
		return "{}\n"
	}
	return string(document) + "\n"
}

func siteUIPrimitiveImports() []string {
	return []string{
		"Badge from ./components/ui/badge",
		"Button from ./components/ui/button",
		"Card, CardContent, CardDescription, CardHeader, CardTitle from ./components/ui/card",
		"Input from ./components/ui/input",
		"Textarea from ./components/ui/textarea",
		"Separator from ./components/ui/separator",
		"Tabs, TabsContent, TabsList, TabsTrigger from ./components/ui/tabs",
		"Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger from ./components/ui/dialog",
	}
}

func siteSourceGuidance() string {
	return "Use local shadcn-style primitives from ./components/ui/* and customize App.tsx, prototype-data.ts, and index.css. Default to black-on-white minimal styling unless the user asks for another direction. Keep package.json, Vite, Tailwind, and scripts managed."
}

func siteIdeaMarkdown(site siteCreateResult) string {
	title := firstNonEmptyString(site.Title, site.Slug)
	summary := firstNonEmptyString(site.Description, title)
	idea := firstNonEmptyString(site.Idea, site.OriginalPrompt, "Refine this file with the user's site idea before publish.")
	return "# Site Idea\n\n## Summary\n" + summary + "\n\n## Original Idea\n" + idea + "\n\n## Audience\n" + firstNonEmptyString(site.Audience, "Unspecified") + "\n\n## Purpose\n" + firstNonEmptyString(site.Purpose, "prototype") + "\n\n## Archetype\n" + firstNonEmptyString(site.Archetype, "landing") + "\n"
}

func siteDesignDocument(site siteCreateResult) string {
	title := html.EscapeString(firstNonEmptyString(site.Title, site.Slug))
	return "# " + title + " DESIGN.md\n\n## Product\n\nEditable scaffold for a website prototype. Replace this file with a request-specific design system before publishing user-facing work.\n\n## Audience\n\nDefine the primary user and what they are trying to accomplish.\n\n## Prototype Scope\n\nDescribe what works in the first publish and what is intentionally deferred.\n\n## Visual Direction\n\nDefault to black-on-white minimal styling: white background, near-black text, quiet gray borders, restrained monochrome controls, and color only when the domain clearly benefits from it. Choose typography, spacing, layout density, interaction feel, and responsive behavior for this specific request.\n\n## Screens\n\nList the screens and states included in the prototype.\n\n## Workflows\n\nDescribe the main interaction paths the user can try.\n\n## Data Model\n\nDefine local state, fake data, PocketBase collections, files, or realtime behavior.\n\n## Implemented Now\n\nReplace this scaffold with the implemented feature set before publishing.\n\n## Next Iterations\n\nRecord follow-up work for longer projects.\n\n## Acceptance Criteria\n\nList the checks that must pass before publish.\n"
}

func toolResultPointer(result agent.ToolResult) *agent.ToolResult {
	return &result
}

func siteSourceWorkspacePath(inputDocument map[string]any) string {
	value, isString := inputDocument["sourceWorkspacePath"].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func defaultSiteSourceWorkspacePath(inputDocument map[string]any) string {
	siteID, isString := inputDocument["siteID"].(string)
	if !isString || strings.TrimSpace(siteID) == "" {
		return ""
	}
	return canonicalSiteSourceWorkspacePath(strings.TrimSpace(siteID))
}

func siteIDFromInputDocument(inputDocument map[string]any) string {
	siteID, isString := inputDocument["siteID"].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(siteID)
}

func canonicalSiteSourceWorkspacePath(siteID string) string {
	if strings.TrimSpace(siteID) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(canonicalSiteProjectWorkspacePath(siteID), "draft"))
}

func canonicalSiteProjectWorkspacePath(siteID string) string {
	if strings.TrimSpace(siteID) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join("home", "sites", strings.TrimSpace(siteID)))
}

func sourceWorkspacePathFromSiteAppWorkspacePath(appWorkspacePath string) string {
	cleanPath := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(appWorkspacePath)), "/")
	if cleanPath == "" {
		return ""
	}
	if strings.HasSuffix(cleanPath, "/app") {
		return strings.TrimSuffix(cleanPath, "/app")
	}
	return ""
}

func siteProjectIDFromPath(path string) string {
	cleanPath := strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
	for _, prefix := range []string{"home/sites/", "workspace/sites/", "sites/"} {
		if siteID := pathSegmentAfterPrefix(cleanPath, prefix); siteID != "" {
			return siteID
		}
	}
	parts := strings.Split(cleanPath, "/")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] == "sites" && parts[index+1] != "" {
			return parts[index+1]
		}
	}
	return ""
}

func pathSegmentAfterPrefix(path string, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	segment, _, _ := strings.Cut(remainder, "/")
	return strings.TrimSpace(segment)
}

func siteWorkspacePathCandidates(sourceWorkspacePath string, siteID string) []string {
	candidates := []string{}
	canonicalPath := canonicalSiteSourceWorkspacePath(siteID)
	if canonicalPath != "" && shouldPreferCanonicalSiteWorkspacePath(sourceWorkspacePath) {
		candidates = append(candidates, canonicalPath)
	}
	if strings.TrimSpace(sourceWorkspacePath) != "" {
		candidates = append(candidates, strings.TrimSpace(sourceWorkspacePath))
	}
	if canonicalPath != "" && !shouldPreferCanonicalSiteWorkspacePath(sourceWorkspacePath) {
		candidates = append(candidates, canonicalPath)
	}
	return stableUniqueStrings(candidates)
}

func shouldPreferCanonicalSiteWorkspacePath(path string) bool {
	cleanPath := strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
	return cleanPath == "" ||
		strings.HasPrefix(cleanPath, "home/sites/") ||
		strings.HasPrefix(cleanPath, "workspace/sites/") ||
		strings.HasPrefix(cleanPath, "workspace/private/people/") ||
		strings.HasPrefix(cleanPath, "sites/")
}

func stableUniqueStrings(values []string) []string {
	seenValues := map[string]bool{}
	uniqueValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		uniqueValues = append(uniqueValues, trimmedValue)
	}
	return uniqueValues
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveSiteProjectSourceWorkspace(toolContext context.Context, request ToolCatalogRequest, workspaceActor security.WorkspaceActor, input siteProjectResolutionInput) (ResolvedWorkspacePath, error) {
	scope := WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, agent.TaskRunIDFromContext(toolContext))
	candidates := siteWorkspacePathCandidates(input.SourceWorkspacePath, firstNonEmptyString(input.SiteID, siteProjectIDFromPath(input.SourceWorkspacePath)))
	if len(candidates) == 0 {
		return ResolvedWorkspacePath{}, errors.New("site sourceWorkspacePath could not be resolved")
	}
	var firstResolvedPath *ResolvedWorkspacePath
	var lastError error
	for _, candidate := range candidates {
		resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).ResolveDirectory(candidate, scope)
		if errorValue != nil {
			lastError = errorValue
			continue
		}
		if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
			lastError = errors.New("current account cannot use this site workspace path: " + resolvedPath.VirtualPath)
			continue
		}
		if firstResolvedPath == nil {
			resolvedPathCopy := resolvedPath
			firstResolvedPath = &resolvedPathCopy
		}
		sourceStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(resolvedPath))
		if errorValue == nil && sourceStat.IsDirectory {
			return resolvedPath, nil
		}
	}
	if firstResolvedPath != nil {
		return *firstResolvedPath, nil
	}
	if lastError != nil {
		return ResolvedWorkspacePath{}, lastError
	}
	return ResolvedWorkspacePath{}, errors.New("site sourceWorkspacePath could not be resolved")
}

func siteSourceBundleExcludeNames() []string {
	return []string{".git", "node_modules", ".vite", ".turbo", ".cache"}
}
