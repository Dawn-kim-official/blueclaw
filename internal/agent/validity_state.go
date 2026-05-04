package agent

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	Passed       bool   `json:"passed"`
	Reason       string `json:"reason,omitempty"`
	path         string
}

func buildArtifactValidityState(artifacts []CompletionArtifact) ValidityState {
	return summarizeArtifactValidity(validateCompletionArtifacts(artifacts))
}

func buildAttachmentValidityState(workspaceRootPath string, attachments []FileAttachment) ValidityState {
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
		path, isCheckable := checkableAttachmentPath(workspaceRootPath, attachment)
		if !isCheckable {
			continue
		}
		checkedArtifacts = append(checkedArtifacts, validateArtifactPath(path, attachmentFilenameForValidity(attachment), relativeWorkspacePath(workspaceRootPath, path)))
	}
	return checkedArtifacts
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
	if !filepath.IsAbs(devicePath) {
		return filepath.Join(rootPath, devicePath), true
	}
	return "", false
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
	switch artifact.Suffix {
	case ".pptx":
		return validatePPTXArtifact(path, artifact)
	case ".pdf":
		return validatePDFArtifact(path, artifact)
	case ".html":
		return validateHTMLArtifact(path, artifact)
	default:
		return validArtifact(artifact)
	}
}

func validatePPTXArtifact(path string, artifact ArtifactValidity) ArtifactValidity {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return invalidArtifact(artifact, "pptx package cannot be opened")
	}
	defer reader.Close()
	requiredEntries := map[string]bool{
		"[Content_Types].xml":  false,
		"ppt/presentation.xml": false,
	}
	slideCount := 0
	for _, file := range reader.File {
		if _, isRequired := requiredEntries[file.Name]; isRequired {
			requiredEntries[file.Name] = true
		}
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slideCount++
		}
	}
	for _, isFound := range requiredEntries {
		if !isFound {
			return invalidArtifact(artifact, "pptx package is missing required presentation entries")
		}
	}
	artifact.SlideCount = slideCount
	if slideCount == 0 {
		return invalidArtifact(artifact, "pptx package has no slides")
	}
	return validArtifact(artifact)
}

func validatePDFArtifact(path string, artifact ArtifactValidity) ArtifactValidity {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return invalidArtifact(artifact, "pdf file cannot be read")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(content), []byte("%PDF")) {
		return invalidArtifact(artifact, "pdf file does not start with PDF header")
	}
	pageCount := countPDFPages(content)
	artifact.PageCount = pageCount
	return validArtifact(artifact)
}

func validateHTMLArtifact(path string, artifact ArtifactValidity) ArtifactValidity {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return invalidArtifact(artifact, "html file cannot be read")
	}
	normalizedContent := strings.ToLower(string(content))
	if !strings.Contains(normalizedContent, "<html") {
		return invalidArtifact(artifact, "html file is missing html element")
	}
	if !strings.Contains(normalizedContent, "<body") {
		return invalidArtifact(artifact, "html file is missing body element")
	}
	return validArtifact(artifact)
}

func countPDFPages(content []byte) int {
	pageCount := bytes.Count(content, []byte("/Type /Page"))
	pageTreeCount := bytes.Count(content, []byte("/Type /Pages"))
	if pageCount > pageTreeCount {
		return pageCount - pageTreeCount
	}
	return bytes.Count(content, []byte("/Page"))
}

func artifactValiditySuffix(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if strings.HasSuffix(trimmedValue, "-notes.txt") {
		return "-notes.txt"
	}
	return strings.ToLower(filepath.Ext(trimmedValue))
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
	if len(validityState.InvalidArtifacts) == 0 {
		return "artifact validity check failed"
	}
	artifact := validityState.InvalidArtifacts[0]
	return "artifact validity check failed for " + firstNonEmptyString(artifact.RelativePath, artifact.Filename) + ": " + artifact.Reason
}
