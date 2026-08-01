package bluecollar

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ValidityState struct {
	Passed           bool               `json:"passed"`
	CheckedArtifacts []ArtifactValidity `json:"checkedArtifacts,omitempty"`
	InvalidArtifacts []ArtifactValidity `json:"invalidArtifacts,omitempty"`
}

type ArtifactValidity struct {
	Filename     string `json:"filename,omitempty"`
	RelativePath string `json:"relativePath,omitempty"`
	Suffix       string `json:"suffix,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	PageCount    int    `json:"pageCount,omitempty"`
	SlideCount   int    `json:"slideCount,omitempty"`
	ModifiedAt   string `json:"modifiedAt,omitempty"`
	Passed       bool   `json:"passed"`
	Reason       string `json:"reason,omitempty"`
	path         string
}

func buildArtifactValidityState(artifacts []CompletionArtifact) ValidityState {
	return summarizeArtifactValidity(validateCompletionArtifacts(artifacts))
}

func buildAttachmentValidityState(workspaceRootPath string, attachments []FileAttachment, _ ...time.Time) ValidityState {
	return summarizeArtifactValidity(validateAttachments(workspaceRootPath, attachments))
}

func summarizeArtifactValidity(checkedArtifacts []ArtifactValidity) ValidityState {
	state := ValidityState{
		Passed:           true,
		CheckedArtifacts: checkedArtifacts,
	}
	for _, artifact := range checkedArtifacts {
		if artifact.Passed {
			continue
		}
		state.Passed = false
		state.InvalidArtifacts = append(state.InvalidArtifacts, artifact)
	}
	return state
}

func validateCompletionArtifacts(artifacts []CompletionArtifact) []ArtifactValidity {
	checkedArtifacts := []ArtifactValidity{}
	for _, artifact := range artifacts {
		checkedArtifacts = append(checkedArtifacts, validateArtifactPath(artifact.path, artifact.Filename, artifact.RelativePath))
	}
	return checkedArtifacts
}

func validateAttachments(workspaceRootPath string, attachments []FileAttachment) []ArtifactValidity {
	checkedArtifacts := []ArtifactValidity{}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ContentBase64) != "" {
			checkedArtifacts = append(checkedArtifacts, validateAttachmentPayload(attachment))
			continue
		}
		path, isCheckable := checkableAttachmentPath(workspaceRootPath, attachment)
		if !isCheckable {
			continue
		}
		checkedArtifacts = append(checkedArtifacts, validateArtifactPath(path, attachmentFilenameForValidity(attachment), relativeWorkspacePath(workspaceRootPath, path)))
	}
	return checkedArtifacts
}

func validateAttachmentPayload(attachment FileAttachment) ArtifactValidity {
	artifact := ArtifactValidity{
		Filename:     attachmentFilenameForValidity(attachment),
		RelativePath: strings.TrimSpace(attachment.DevicePath),
		Suffix:       artifactValiditySuffix(firstNonEmptyString(attachment.Filename, attachment.DevicePath)),
		SizeBytes:    attachment.SizeBytes,
		path:         strings.TrimSpace(attachment.DevicePath),
	}
	document, errorValue := base64.StdEncoding.DecodeString(strings.TrimSpace(attachment.ContentBase64))
	if errorValue != nil {
		return invalidArtifact(artifact, "attachment payload is not readable")
	}
	if len(document) == 0 {
		return invalidArtifact(artifact, "artifact file is empty")
	}
	if artifact.SizeBytes == 0 {
		artifact.SizeBytes = int64(len(document))
	}
	if reason := validateArtifactFormatsBytes(document, attachment.Filename, attachment.DevicePath); reason != "" {
		return invalidArtifact(artifact, reason)
	}
	return validArtifact(artifact)
}

func checkableAttachmentPath(workspaceRootPath string, attachment FileAttachment) (string, bool) {
	devicePath := strings.TrimSpace(attachment.DevicePath)
	if devicePath == "" {
		return "", false
	}
	if _, errorValue := os.Stat(devicePath); errorValue == nil {
		return devicePath, true
	}
	rootPath := strings.TrimSpace(workspaceRootPath)
	if rootPath == "" {
		return "", false
	}
	if filepath.IsAbs(devicePath) && strings.HasPrefix(filepath.Clean(devicePath), filepath.Clean(rootPath)+string(os.PathSeparator)) {
		return devicePath, true
	}
	if strings.HasPrefix(devicePath, "/workspace/") {
		return filepath.Join(rootPath, strings.TrimPrefix(devicePath, "/workspace/")), true
	}
	if strings.HasPrefix(filepath.ToSlash(devicePath), "artifacts/") {
		if path, isFound := firstRequesterArtifactPath(rootPath, devicePath); isFound {
			return path, true
		}
	}
	if !filepath.IsAbs(devicePath) {
		return filepath.Join(rootPath, devicePath), true
	}
	return "", false
}

func firstRequesterArtifactPath(workspaceRootPath string, devicePath string) (string, bool) {
	relativeArtifactPath := strings.TrimPrefix(filepath.ToSlash(devicePath), "artifacts/")
	matches, errorValue := filepath.Glob(filepath.Join(workspaceRootPath, "private", "people", "*", "artifacts", filepath.FromSlash(relativeArtifactPath)))
	if errorValue != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

func attachmentFilenameForValidity(attachment FileAttachment) string {
	if strings.TrimSpace(attachment.Filename) != "" {
		return strings.TrimSpace(attachment.Filename)
	}
	return filepath.Base(strings.TrimSpace(attachment.DevicePath))
}

func validateArtifactPath(path string, filename string, relativePath string) ArtifactValidity {
	artifact := ArtifactValidity{
		Filename:     firstNonEmptyString(strings.TrimSpace(filename), filepath.Base(path)),
		RelativePath: strings.TrimSpace(relativePath),
		Suffix:       artifactValiditySuffix(firstNonEmptyString(filename, path)),
		path:         path,
	}
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return invalidArtifact(artifact, "artifact file is not readable")
	}
	if !fileInformation.Mode().IsRegular() {
		return invalidArtifact(artifact, "artifact path is not a regular file")
	}
	artifact.SizeBytes = fileInformation.Size()
	if artifact.SizeBytes == 0 {
		return invalidArtifact(artifact, "artifact file is empty")
	}
	if reason := validateArtifactFormatsPath(path, filename, path); reason != "" {
		return invalidArtifact(artifact, reason)
	}
	return validArtifact(artifact)
}

func artifactValiditySuffix(value string) string {
	filename := filepath.Base(strings.TrimSpace(value))
	lowercaseFilename := strings.ToLower(filename)
	if strings.HasSuffix(lowercaseFilename, "-notes.txt") {
		return "-notes.txt"
	}
	return strings.ToLower(filepath.Ext(filename))
}

func validArtifact(artifact ArtifactValidity) ArtifactValidity {
	artifact.Passed = true
	artifact.Reason = ""
	return artifact
}

func invalidArtifact(artifact ArtifactValidity, reason string) ArtifactValidity {
	artifact.Passed = false
	artifact.Reason = reason
	return artifact
}

func validityFailureMessage(validityState ValidityState) string {
	lines := []string{"artifact validity check failed"}
	for _, artifact := range validityState.InvalidArtifacts {
		label := firstNonEmptyString(artifact.RelativePath, artifact.Filename)
		if strings.TrimSpace(label) == "" {
			label = "artifact"
		}
		lines = append(lines, label+": "+artifact.Reason)
	}
	return strings.Join(lines, "\n")
}
