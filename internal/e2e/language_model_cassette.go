package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"blueclaw/internal/llm"
)

type LanguageModelCassette struct {
	Entries []LanguageModelCassetteEntry `json:"entries"`
}

type LanguageModelCassetteEntry struct {
	Kind         string    `json:"kind"`
	SchemaName   string    `json:"schemaName,omitempty"`
	ProviderName string    `json:"providerName,omitempty"`
	ModelName    string    `json:"modelName,omitempty"`
	Content      string    `json:"content"`
	UsedFallback bool      `json:"usedFallback,omitempty"`
	Usage        llm.Usage `json:"usage,omitempty"`
}

type RecordingLanguageModel struct {
	mutex    sync.Mutex
	provider llm.LanguageModelProvider
	entries  []LanguageModelCassetteEntry
	requests []llm.StructuredResponseRequest
}

type ReplayingLanguageModel struct {
	mutex    sync.Mutex
	entries  []LanguageModelCassetteEntry
	position int
	requests []llm.StructuredResponseRequest
	returned []LanguageModelCassetteEntry
}

func NewRecordingLanguageModel(provider llm.LanguageModelProvider) *RecordingLanguageModel {
	return &RecordingLanguageModel{provider: provider}
}

func NewReplayingLanguageModel(cassette LanguageModelCassette) *ReplayingLanguageModel {
	return &ReplayingLanguageModel{entries: append([]LanguageModelCassetteEntry{}, cassette.Entries...)}
}

func LoadLanguageModelCassette(path string) (LanguageModelCassette, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return LanguageModelCassette{}, errorValue
	}
	var cassette LanguageModelCassette
	if errorValue := json.Unmarshal(document, &cassette); errorValue != nil {
		return LanguageModelCassette{}, errorValue
	}
	return cassette, nil
}

func SaveLanguageModelCassette(path string, cassette LanguageModelCassette) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("cassette path is required")
	}
	document, errorValue := json.MarshalIndent(cassette, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, document, 0600)
}

func (languageModel *RecordingLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	response, errorValue := languageModel.provider.GenerateResponse(ctx, prompt)
	if errorValue != nil {
		return "", errorValue
	}
	languageModel.appendEntry(LanguageModelCassetteEntry{Kind: "text", Content: response})
	return response, nil
}

func (languageModel *RecordingLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.appendRequest(request)
	response, errorValue := languageModel.provider.GenerateStructuredResponse(ctx, request)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	languageModel.appendEntry(LanguageModelCassetteEntry{
		Kind:         "structured",
		SchemaName:   strings.TrimSpace(request.StructuredOutputSchema.Name),
		ProviderName: response.ProviderName,
		ModelName:    response.ModelName,
		Content:      response.Content,
		UsedFallback: response.UsedFallback,
		Usage:        response.Usage,
	})
	return response, nil
}

func (languageModel *RecordingLanguageModel) Cassette() LanguageModelCassette {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return LanguageModelCassette{Entries: append([]LanguageModelCassetteEntry{}, languageModel.entries...)}
}

func (languageModel *RecordingLanguageModel) RequestCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.requests)
}

func (languageModel *RecordingLanguageModel) RequestsSince(startIndex int) []llm.StructuredResponseRequest {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.requests) {
		startIndex = 0
	}
	return append([]llm.StructuredResponseRequest{}, languageModel.requests[startIndex:]...)
}

func (languageModel *RecordingLanguageModel) appendEntry(entry LanguageModelCassetteEntry) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.entries = append(languageModel.entries, entry)
}

func (languageModel *RecordingLanguageModel) appendRequest(request llm.StructuredResponseRequest) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
}

func (languageModel *ReplayingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	entry, errorValue := languageModel.nextEntry("text", "")
	if errorValue != nil {
		return "", errorValue
	}
	return entry.Content, nil
}

func (languageModel *ReplayingLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.appendRequest(request)
	entry, errorValue := languageModel.nextEntry("structured", strings.TrimSpace(request.StructuredOutputSchema.Name))
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	return llm.StructuredResponse{
		ProviderName: entry.ProviderName,
		ModelName:    entry.ModelName,
		Content:      entry.Content,
		UsedFallback: entry.UsedFallback,
		Usage:        entry.Usage,
	}, nil
}

func (languageModel *ReplayingLanguageModel) ReturnedEntries() []LanguageModelCassetteEntry {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return append([]LanguageModelCassetteEntry{}, languageModel.returned...)
}

func (languageModel *ReplayingLanguageModel) RequestCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.requests)
}

func (languageModel *ReplayingLanguageModel) RequestsSince(startIndex int) []llm.StructuredResponseRequest {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.requests) {
		startIndex = 0
	}
	return append([]llm.StructuredResponseRequest{}, languageModel.requests[startIndex:]...)
}

func (languageModel *ReplayingLanguageModel) nextEntry(kind string, schemaName string) (LanguageModelCassetteEntry, error) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if languageModel.position >= len(languageModel.entries) {
		return LanguageModelCassetteEntry{}, errors.New("language model cassette response queue is empty")
	}
	entry := languageModel.entries[languageModel.position]
	if entry.Kind != kind {
		return LanguageModelCassetteEntry{}, errors.New("language model cassette expected " + kind + " response, got " + entry.Kind)
	}
	if schemaName != "" && strings.TrimSpace(entry.SchemaName) != schemaName {
		return LanguageModelCassetteEntry{}, errors.New("language model cassette expected schema " + schemaName + ", got " + entry.SchemaName)
	}
	languageModel.position++
	languageModel.returned = append(languageModel.returned, entry)
	return entry, nil
}

func (languageModel *ReplayingLanguageModel) appendRequest(request llm.StructuredResponseRequest) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
}
