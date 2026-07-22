package agent

import (
	"encoding/json"
	"strings"
)

const siteBuiltRecoveryPrecondition = "site_built"

func sitePublishPrerequisiteFailure(observations []turnObservation, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	operationName := effectiveObservationToolName(actionDocument.ToolName, actionDocument.ToolInput)
	if !siteOperationRequiresFreshBuild(operationName) {
		return turnObservation{}, false
	}
	if siteHasFreshBuild(observations) {
		return turnObservation{}, false
	}
	message := "site.publish requires a fresh build only after a structural change under the site's app/src or scaffold/app config files. Content edits to app/public/site-content.json publish directly without a build. Since a build-requiring change was made, run terminal.run with workingDirectoryPath set to the site's appWorkspacePath and command \"bun scripts/build.ts\", then retry " + operationName + "."
	observation := newFailureObservation(observationID, "continue", operationName, message, FailureInvalidInput, FailureCodes.InvalidInput, "workflow_prerequisite")
	observation.ToolInputKey = canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
	observation.AttemptFingerprint = attemptFingerprint(observation.ToolInputKey, observation.FailureCode())
	observation.Failure.Retryable = true
	observation.Failure.SafeRetry = true
	observation.Failure.RequiredPreconditions = []string{siteBuiltRecoveryPrecondition}
	return observation, true
}

func siteOperationRequiresFreshBuild(operationName string) bool {
	switch strings.TrimSpace(operationName) {
	case "site.publish", "site.preview":
		return true
	default:
		return false
	}
}

func siteHasFreshBuild(observations []turnObservation) bool {
	sourceChangeIndex := latestSiteSourceChangeObservationIndex(observations)
	if sourceChangeIndex < 0 {
		return true
	}
	return latestSuccessfulSiteBuildObservationIndexAfter(observations, sourceChangeIndex) >= 0
}

func latestSiteSourceChangeObservationIndex(observations []turnObservation) int {
	latestIndex := -1
	for index, observation := range observations {
		if observation.Failed() || observation.Action != "continue" {
			continue
		}
		if observationIsSiteSourceChange(observation) {
			latestIndex = index
		}
	}
	return latestIndex
}

func latestSuccessfulSiteBuildObservationIndexAfter(observations []turnObservation, sourceChangeIndex int) int {
	latestIndex := -1
	for index, observation := range observations {
		if index <= sourceChangeIndex || observation.Failed() || observation.Action != "continue" {
			continue
		}
		if observation.Tool == "terminal.run" && terminalObservationIsSiteBuild(observation) {
			latestIndex = index
		}
	}
	return latestIndex
}

func observationIsSiteSourceChange(observation turnObservation) bool {
	switch strings.TrimSpace(observation.Tool) {
	case FileWriteToolName, FileEditToolName, FileDeleteToolName:
		return pathRequiresSiteBuildBeforePublish(observationPath(observation))
	default:
		return false
	}
}

func terminalObservationIsSiteBuild(observation turnObservation) bool {
	input := terminalObservationInput(observation.ToolInputKey)
	if !pathLooksLikeSiteAppWorkspace(input.WorkingDirectory) {
		return false
	}
	return terminalCommandRunsSiteBuild(input.Command)
}

func terminalCommandRunsSiteBuild(command string) bool {
	tokens := normalizedTerminalCommandTokens(command)
	for index, token := range tokens {
		if token == "bun" && nextTokensEqual(tokens, index, "scripts/build.ts") {
			return true
		}
		if token == "bun" && nextTokensEqual(tokens, index, "run", "build") {
			return true
		}
		if token == "npm" && nextTokensEqual(tokens, index, "run", "build") {
			return true
		}
		if token == "pnpm" && nextTokensEqual(tokens, index, "run", "build") {
			return true
		}
		if token == "yarn" && nextTokensEqual(tokens, index, "build") {
			return true
		}
	}
	if terminalCommandCreatesSiteBuildOutput(tokens) {
		return true
	}
	return false
}

func normalizedTerminalCommandTokens(command string) []string {
	tokens := []string{}
	for _, token := range terminalCommandTokens(command) {
		normalizedToken := strings.Trim(strings.ToLower(strings.TrimSpace(token)), `"'`)
		if normalizedToken != "" {
			tokens = append(tokens, normalizedToken)
		}
	}
	return tokens
}

func nextTokensEqual(tokens []string, index int, expectedTokens ...string) bool {
	if len(tokens) <= index+len(expectedTokens) {
		return false
	}
	for offset, expectedToken := range expectedTokens {
		if tokens[index+offset+1] != expectedToken {
			return false
		}
	}
	return true
}

func terminalCommandCreatesSiteBuildOutput(tokens []string) bool {
	return tokenSequenceExists(tokens, "mkdir", "-p", "dist") && tokenListContainsSiteBuildOutput(tokens)
}

func tokenSequenceExists(tokens []string, expectedTokens ...string) bool {
	if len(expectedTokens) == 0 || len(tokens) < len(expectedTokens) {
		return false
	}
	for index := 0; index <= len(tokens)-len(expectedTokens); index++ {
		if tokensMatch(tokens[index:index+len(expectedTokens)], expectedTokens) {
			return true
		}
	}
	return false
}

func tokensMatch(tokens []string, expectedTokens []string) bool {
	if len(tokens) != len(expectedTokens) {
		return false
	}
	for index, token := range tokens {
		if token != expectedTokens[index] {
			return false
		}
	}
	return true
}

func tokenListContainsSiteBuildOutput(tokens []string) bool {
	for _, token := range tokens {
		normalizedToken := strings.Trim(strings.TrimSpace(filepathSlash(token)), `"'`)
		if normalizedToken == "dist/index.html" || strings.HasSuffix(normalizedToken, "/dist/index.html") {
			return true
		}
	}
	return false
}

func observationPath(observation turnObservation) string {
	if path := observationOutputPath(observation); path != "" {
		return path
	}
	var payload map[string]any
	_, canonicalInput, found := strings.Cut(observation.ToolInputKey, "\x00")
	if !found || json.Unmarshal([]byte(canonicalInput), &payload) != nil {
		return ""
	}
	return strings.TrimSpace(stringField(payload, "path"))
}

func pathLooksLikeSiteWorkspace(path string) bool {
	normalizedPath := strings.TrimSpace(filepathSlash(path))
	return strings.Contains(normalizedPath, "/sites/")
}

func pathLooksLikeSiteAppWorkspace(path string) bool {
	normalizedPath := strings.TrimSuffix(strings.TrimSpace(filepathSlash(path)), "/")
	return pathLooksLikeSiteWorkspace(normalizedPath) && strings.HasSuffix(normalizedPath, "/app")
}

func pathRequiresSiteBuildBeforePublish(path string) bool {
	normalizedPath := strings.TrimSpace(filepathSlash(path))
	if !pathLooksLikeSiteWorkspace(normalizedPath) {
		return false
	}
	if pathIsSiteDesignOrControlFile(normalizedPath) {
		return false
	}
	appRelativePath, isUnderSiteAppDirectory := siteAppRelativePath(normalizedPath)
	if !isUnderSiteAppDirectory {
		return false
	}
	return !strings.HasPrefix(appRelativePath, "public/")
}

func pathIsSiteDesignOrControlFile(path string) bool {
	return strings.HasSuffix(path, "/DESIGN.md") || strings.Contains(path, "/.internkim/")
}

func siteAppRelativePath(path string) (string, bool) {
	const appDirectorySegment = "/app/"
	appDirectoryIndex := strings.Index(path, appDirectorySegment)
	if appDirectoryIndex < 0 {
		return "", false
	}
	return path[appDirectoryIndex+len(appDirectorySegment):], true
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
