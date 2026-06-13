package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const DefaultPinnedMemoryHardLimitCharacterCount = 6000
const DefaultPinnedMemoryCompressionTargetCharacterCount = 3500

var safeMemoryPathSegmentPattern = regexp.MustCompile(`[^a-z0-9._-]+`)

type MarkdownStore struct {
	rootPath                        string
	hardLimitCharacterCount         int
	compressionTargetCharacterCount int
	compressor                      MarkdownMemoryCompressor
}

type MarkdownMemoryCompressionRequest struct {
	PersonID       string
	CurrentContent string
	NewContent     string
	HardLimit      int
	TargetLength   int
}

type MarkdownMemoryCompressor interface {
	CompressMemory(context.Context, MarkdownMemoryCompressionRequest) (string, error)
}

func NewMarkdownStore(rootPath string, hardLimitCharacterCount int) *MarkdownStore {
	cleanRootPath := strings.TrimSpace(rootPath)
	if cleanRootPath == "" {
		cleanRootPath = filepath.Join("/workspace", ".blueclaw", "memory")
	}
	if hardLimitCharacterCount <= 0 {
		hardLimitCharacterCount = DefaultPinnedMemoryHardLimitCharacterCount
	}
	return &MarkdownStore{
		rootPath:                        cleanRootPath,
		hardLimitCharacterCount:         hardLimitCharacterCount,
		compressionTargetCharacterCount: DefaultPinnedMemoryCompressionTargetCharacterCount,
	}
}

func (store *MarkdownStore) UseCompressor(compressor MarkdownMemoryCompressor, targetCharacterCount int) {
	if store == nil {
		return
	}
	store.compressor = compressor
	if targetCharacterCount > 0 {
		store.compressionTargetCharacterCount = targetCharacterCount
	}
}

func (store *MarkdownStore) LoadPinnedMemory(ctx context.Context, personID string) ([]MemoryFact, error) {
	if store == nil {
		return nil, nil
	}
	return store.LoadPinnedMemoryForNamespaces(ctx, []MemoryNamespace{UserNamespace(personID)})
}

func (store *MarkdownStore) LoadPinnedMemoryForNamespaces(ctx context.Context, namespaces []MemoryNamespace) ([]MemoryFact, error) {
	if store == nil {
		return nil, nil
	}
	memoryFacts := []MemoryFact{}
	for _, namespace := range namespaces {
		memoryFact, isFound, errorValue := store.readNamespaceMemory(ctx, namespace)
		if errorValue != nil {
			return nil, errorValue
		}
		if isFound {
			memoryFacts = append(memoryFacts, memoryFact)
		}
	}
	return memoryFacts, nil
}

func (store *MarkdownStore) MergePersonMemory(ctx context.Context, personID string, content string) (bool, error) {
	if store == nil {
		return false, nil
	}
	trimmedContent := compactMarkdownWhitespace(content)
	if trimmedContent == "" {
		return false, nil
	}
	namespace := UserNamespace(personID)
	path, isSupported := store.namespacePath(namespace)
	if !isSupported || strings.TrimSpace(personID) == "" {
		return false, nil
	}
	if errorValue := ctx.Err(); errorValue != nil {
		return false, errorValue
	}
	currentContent, errorValue := readOptionalFile(path)
	if errorValue != nil {
		return false, errorValue
	}
	nextContent, errorValue := store.mergeMarkdownMemory(ctx, strings.TrimSpace(currentContent), trimmedContent, strings.TrimSpace(personID))
	if errorValue != nil {
		return false, errorValue
	}
	if nextContent == strings.TrimSpace(currentContent) {
		return false, nil
	}
	return true, writeFileAtomically(path, nextContent+"\n")
}

func (store *MarkdownStore) readNamespaceMemory(ctx context.Context, namespace MemoryNamespace) (MemoryFact, bool, error) {
	path, isSupported := store.namespacePath(namespace)
	if !isSupported {
		return MemoryFact{}, false, nil
	}
	if errorValue := ctx.Err(); errorValue != nil {
		return MemoryFact{}, false, errorValue
	}
	content, errorValue := readOptionalFile(path)
	if errorValue != nil {
		return MemoryFact{}, false, errorValue
	}
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return MemoryFact{}, false, nil
	}
	return MemoryFact{
		FactID:            "pinned:" + namespace.NamespaceID,
		ScopeType:         namespace.ScopeType,
		NamespaceID:       namespace.NamespaceID,
		Content:           trimmedContent,
		SourceKind:        MemorySourceKindPinned,
		ValidAt:           time.Now().UTC(),
		SecurityLevelRank: namespace.SecurityLevelRank,
		RequiredClasses:   append([]string{}, namespace.RequiredClasses...),
	}, true, nil
}

func (store *MarkdownStore) namespacePath(namespace MemoryNamespace) (string, bool) {
	switch namespace.ScopeType {
	case ScopeTypeUser:
		return filepath.Join(store.rootPath, "people", safeMemoryPathSegment(namespace.ScopePersonID), "MEMORY.md"), true
	default:
		return "", false
	}
}

func (store *MarkdownStore) mergeMarkdownMemory(ctx context.Context, currentContent string, newContent string, personID string) (string, error) {
	lines := markdownMemoryLines(currentContent)
	newLine := "- " + newContent
	if containsMarkdownLine(lines, newLine) {
		return strings.TrimSpace(currentContent), nil
	}
	mergedContent := strings.TrimSpace(strings.Join(append(lines, newLine), "\n"))
	if runeCount(mergedContent) <= store.hardLimitCharacterCount {
		return mergedContent, nil
	}
	return store.compressMarkdownMemory(ctx, currentContent, newContent, personID)
}

func (store *MarkdownStore) compressMarkdownMemory(ctx context.Context, currentContent string, newContent string, personID string) (string, error) {
	if store.compressor == nil {
		return "", errors.New("memory compressor is unavailable")
	}
	compressedContent, errorValue := store.compressor.CompressMemory(ctx, MarkdownMemoryCompressionRequest{
		PersonID:       personID,
		CurrentContent: currentContent,
		NewContent:     newContent,
		HardLimit:      store.hardLimitCharacterCount,
		TargetLength:   store.compressionTargetCharacterCount,
	})
	if errorValue != nil {
		return "", errorValue
	}
	trimmedContent := strings.TrimSpace(compressedContent)
	if trimmedContent == "" || runeCount(trimmedContent) > store.hardLimitCharacterCount {
		return "", errors.New("memory compressor returned invalid content")
	}
	return trimmedContent, nil
}

func markdownMemoryLines(content string) []string {
	lines := []string{"# Memory"}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" || trimmedLine == "# Memory" {
			continue
		}
		lines = append(lines, trimmedLine)
	}
	return lines
}

func containsMarkdownLine(lines []string, expectedLine string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) == expectedLine {
			return true
		}
	}
	return false
}

func readOptionalFile(path string) (string, error) {
	document, errorValue := os.ReadFile(path)
	if os.IsNotExist(errorValue) {
		return "", nil
	}
	if errorValue != nil {
		return "", errorValue
	}
	return string(document), nil
}

func writeFileAtomically(path string, content string) error {
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		return errorValue
	}
	temporaryFile, errorValue := os.CreateTemp(filepath.Dir(path), ".MEMORY.md.*")
	if errorValue != nil {
		return errorValue
	}
	temporaryPath := temporaryFile.Name()
	if _, errorValue = temporaryFile.WriteString(content); errorValue != nil {
		_ = temporaryFile.Close()
		_ = os.Remove(temporaryPath)
		return errorValue
	}
	if errorValue = temporaryFile.Close(); errorValue != nil {
		_ = os.Remove(temporaryPath)
		return errorValue
	}
	return os.Rename(temporaryPath, path)
}

func safeMemoryPathSegment(value string) string {
	normalizedValue := strings.ToLower(strings.TrimSpace(value))
	cleanValue := strings.Trim(safeMemoryPathSegmentPattern.ReplaceAllString(normalizedValue, "-"), "-")
	if cleanValue == normalizedValue && cleanValue != "" {
		return cleanValue
	}
	hash := sha256.Sum256([]byte(normalizedValue))
	suffix := hex.EncodeToString(hash[:])[:12]
	if cleanValue == "" {
		return suffix
	}
	if len(cleanValue) > 40 {
		cleanValue = cleanValue[:40]
	}
	return cleanValue + "-" + suffix
}

func compactMarkdownWhitespace(content string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
}

func runeCount(content string) int {
	return len([]rune(content))
}
