package agent

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
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

type deckIntentManifest struct {
	OutputSlug          string   `json:"output_slug"`
	Mode                string   `json:"mode"`
	Topic               string   `json:"topic"`
	SlideIntent         string   `json:"slide_intent"`
	RequestedSlideCount int      `json:"requested_slide_count"`
	RequestedFormats    []string `json:"requested_formats"`
	SlideCount          int      `json:"slide_count"`
}

func buildArtifactValidityState(artifacts []CompletionArtifact) ValidityState {
	return summarizeArtifactValidity(validateCompletionArtifacts(artifacts))
}

func buildAttachmentValidityState(workspaceRootPath string, attachments []FileAttachment, minimumModifiedAt time.Time) ValidityState {
	return summarizeArtifactValidity(validateAttachments(workspaceRootPath, attachments, minimumModifiedAt))
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
		checkedArtifacts = append(checkedArtifacts, validateArtifactPath(artifact.path, artifact.Filename, artifact.RelativePath, time.Time{}))
	}
	return checkedArtifacts
}

func validateAttachments(workspaceRootPath string, attachments []FileAttachment, minimumModifiedAt time.Time) []ArtifactValidity {
	checkedArtifacts := []ArtifactValidity{}
	for _, attachment := range attachments {
		path, isCheckable := checkableAttachmentPath(workspaceRootPath, attachment)
		if !isCheckable {
			continue
		}
		checkedArtifacts = append(checkedArtifacts, validateArtifactPath(path, attachmentFilenameForValidity(attachment), relativeWorkspacePath(workspaceRootPath, path), minimumModifiedAt))
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

func validateArtifactPath(path string, filename string, relativePath string, minimumModifiedAt time.Time) ArtifactValidity {
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
	artifact.ModifiedAt = fileInformation.ModTime().UTC().Format(time.RFC3339Nano)
	if !minimumModifiedAt.IsZero() && fileInformation.ModTime().Before(minimumModifiedAt) {
		return invalidArtifact(artifact, "artifact was not created during this task run")
	}
	if artifact.SizeBytes == 0 {
		return invalidArtifact(artifact, "artifact file is empty")
	}
	switch artifact.Suffix {
	case ".pptx":
		return validateArtifactIntent(path, validatePPTXArtifact(path, artifact))
	case ".pdf":
		return validateArtifactIntent(path, validatePDFArtifact(path, artifact))
	case ".html":
		return validateArtifactIntent(path, validateHTMLArtifact(path, artifact))
	case "-notes.txt":
		return validateArtifactIntent(path, validateNotesArtifact(path, artifact))
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
	artifact.SlideCount = countHTMLSlides(normalizedContent)
	return validArtifact(artifact)
}

func validateNotesArtifact(path string, artifact ArtifactValidity) ArtifactValidity {
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return invalidArtifact(artifact, "notes file cannot be read")
	}
	artifact.SlideCount = countNotesSlides(string(content))
	return validArtifact(artifact)
}

func validateArtifactIntent(path string, artifact ArtifactValidity) ArtifactValidity {
	if !artifact.Passed {
		return artifact
	}
	manifestPath, isRequired := deckIntentManifestPath(path)
	manifest, isFound, reason := readDeckIntentManifest(manifestPath)
	if reason != "" {
		return invalidArtifact(artifact, reason)
	}
	if !isFound {
		if isRequired {
			return invalidArtifact(artifact, "deck intent manifest is missing")
		}
		return artifact
	}
	if reason := validateArtifactAgainstDeckIntent(path, artifact, manifest); reason != "" {
		return invalidArtifact(artifact, reason)
	}
	return artifact
}

func deckIntentManifestPath(path string) (string, bool) {
	directoryPath := filepath.Dir(path)
	filename := filepath.Base(path)
	slug := strings.TrimSuffix(filename, "-notes.txt")
	if slug == filename {
		slug = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	manifestPath := filepath.Join(directoryPath, slug+"-intent.json")
	_, presentationError := os.Stat(filepath.Join(directoryPath, "presentation.md"))
	return manifestPath, presentationError == nil && strings.Contains(filepath.ToSlash(filepath.Clean(path)), "/.blueclaw/tmp/")
}

func readDeckIntentManifest(path string) (deckIntentManifest, bool, string) {
	document, errorValue := os.ReadFile(path)
	if os.IsNotExist(errorValue) {
		return deckIntentManifest{}, false, ""
	}
	if errorValue != nil {
		return deckIntentManifest{}, false, "deck intent manifest is not readable"
	}
	var manifest deckIntentManifest
	if errorValue := json.Unmarshal(document, &manifest); errorValue != nil {
		return deckIntentManifest{}, false, "deck intent manifest is not valid json"
	}
	return manifest, true, ""
}

func validateArtifactAgainstDeckIntent(path string, artifact ArtifactValidity, manifest deckIntentManifest) string {
	if !artifactSlugMatchesManifest(artifact.Filename, manifest.OutputSlug) {
		return "artifact filename does not match deck intent output_slug"
	}
	if !artifactSuffixAllowedByManifest(artifact.Suffix, manifest.RequestedFormats) {
		return "artifact suffix is not requested by deck intent"
	}
	if manifest.RequestedSlideCount > 0 && artifact.SlideCount > 0 && artifact.SlideCount != manifest.RequestedSlideCount {
		return "artifact slide count does not match deck intent"
	}
	text := artifactIntentText(path, artifact.Suffix)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	normalizedText := strings.ToLower(text)
	if normalizedMode := strings.ToLower(strings.TrimSpace(manifest.Mode)); normalizedMode != "capabilities" && containsForbiddenSampleToken(normalizedText) {
		return "non-capabilities artifact contains built-in capability sample text"
	}
	if !containsIntentToken(normalizedText, manifest.Topic) {
		return "artifact text does not contain deck topic"
	}
	if !containsIntentToken(normalizedText, manifest.SlideIntent) {
		return "artifact text does not contain deck intent"
	}
	return ""
}

func artifactSlugMatchesManifest(filename string, outputSlug string) bool {
	normalizedOutputSlug := strings.TrimSpace(outputSlug)
	if normalizedOutputSlug == "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(filename), normalizedOutputSlug+".") ||
		strings.HasPrefix(strings.TrimSpace(filename), normalizedOutputSlug+"-")
}

func artifactSuffixAllowedByManifest(suffix string, requestedFormats []string) bool {
	if len(requestedFormats) == 0 {
		return true
	}
	for _, requestedFormat := range requestedFormats {
		if requestedFormatSuffix(strings.TrimSpace(requestedFormat)) == suffix {
			return true
		}
	}
	return false
}

func requestedFormatSuffix(format string) string {
	switch strings.TrimPrefix(strings.ToLower(format), ".") {
	case "pptx":
		return ".pptx"
	case "pdf":
		return ".pdf"
	case "html":
		return ".html"
	case "note", "notes", "txt", "speaker-notes":
		return "-notes.txt"
	default:
		return ""
	}
}

func containsForbiddenSampleToken(value string) bool {
	return strings.Contains(value, "internkim capability deck") ||
		strings.Contains(value, "김인턴이 할 수 있는 일")
}

func containsIntentToken(value string, intent string) bool {
	tokens := intentTokens(intent)
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func intentTokens(value string) []string {
	tokens := []string{}
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}) {
		if len([]rune(token)) >= 3 || containsHangul(token) {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func containsHangul(value string) bool {
	for _, character := range value {
		if character >= '가' && character <= '힣' {
			return true
		}
	}
	return false
}

func artifactIntentText(path string, suffix string) string {
	switch suffix {
	case ".pptx":
		return pptxText(path)
	case ".html", "-notes.txt":
		content, _ := os.ReadFile(path)
		return string(content)
	default:
		return ""
	}
}

func pptxText(path string) string {
	reader, errorValue := zip.OpenReader(path)
	if errorValue != nil {
		return ""
	}
	defer reader.Close()
	builder := strings.Builder{}
	for _, file := range reader.File {
		if !strings.HasPrefix(file.Name, "ppt/slides/slide") || !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		fileReader, errorValue := file.Open()
		if errorValue != nil {
			continue
		}
		document, _ := io.ReadAll(fileReader)
		_ = fileReader.Close()
		builder.WriteString(" ")
		builder.WriteString(string(document))
	}
	return builder.String()
}

func countHTMLSlides(content string) int {
	if count := strings.Count(content, "data-marpit-pagination="); count > 0 {
		return count
	}
	return strings.Count(content, "bespoke-marp-slide")
}

func countNotesSlides(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# Slide ") {
			count++
		}
	}
	return count
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
