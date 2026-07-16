package agent

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/llm"
)

const defaultEmbeddingModelName = "embedding.create"
const skillSearchDocumentVersion = "skill-description-v2"
const maxGeneratedSkillSearchQueries = 5

type SkillRetriever interface {
	Retrieve(context.Context, AgentRequest, []SkillInstruction, int) SkillRetrievalResult
	Search(context.Context, AgentRequest, []SkillInstruction, SkillSearchQuerySet, int) SkillRetrievalResult
	Refresh(context.Context, []SkillInstruction)
}

type SkillSearchQuery struct {
	Description string `json:"description"`
}

type SkillSearchQuerySet struct {
	Queries []SkillSearchQuery `json:"queries"`
}

type SkillSearchResult struct {
	Skills []SkillSearchResultItem `json:"skills"`
}

type SkillSearchResultItem struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Prompt      string          `json:"prompt,omitempty"`
	Score       float64         `json:"score"`
	Tools       []string        `json:"tools"`
	SourcePath  string          `json:"sourcePath,omitempty"`
	Completion  SkillCompletion `json:"completion,omitempty"`
}

type SkillRetrievalResult struct {
	RetrievalMode      string
	IndexStatus        string
	CandidateCount     int
	QueryDescriptions  []string
	SelectedCandidates []SkillCandidate
}

type SkillCandidate struct {
	Name   string
	Score  float64
	Reason string
	Source InstructionSource
}

type SkillSearchDocument struct {
	SkillName      string    `json:"skillName"`
	SourcePath     string    `json:"sourcePath"`
	SourceSHA256   string    `json:"sourceSHA256"`
	SearchText     string    `json:"searchText"`
	EmbeddingModel string    `json:"embeddingModel"`
	Embedding      []float32 `json:"embedding"`
	IndexedAt      time.Time `json:"indexedAt"`
}

type EmbeddingSkillRetriever struct {
	EmbeddingProvider llm.EmbeddingProvider
	IndexPath         string
	EmbeddingModel    string
	mutex             sync.Mutex
	documents         []SkillSearchDocument
	isLoaded          bool
}

func NewEmbeddingSkillRetriever(embeddingProvider llm.EmbeddingProvider, indexPath string) *EmbeddingSkillRetriever {
	return &EmbeddingSkillRetriever{
		EmbeddingProvider: embeddingProvider,
		IndexPath:         strings.TrimSpace(indexPath),
		EmbeddingModel:    defaultEmbeddingModelName,
	}
}

func (skillRetriever *EmbeddingSkillRetriever) Retrieve(ctx context.Context, request AgentRequest, skillInstructions []SkillInstruction, limit int) SkillRetrievalResult {
	return skillRetriever.Search(ctx, request, skillInstructions, SkillSearchQuerySet{
		Queries: []SkillSearchQuery{{Description: skillSelectionPrompt(request)}},
	}, limit)
}

func (skillRetriever *EmbeddingSkillRetriever) Search(ctx context.Context, request AgentRequest, skillInstructions []SkillInstruction, querySet SkillSearchQuerySet, limit int) SkillRetrievalResult {
	if directCandidate, isFound := directSkillCandidate(request, skillInstructions); isFound {
		return SkillRetrievalResult{
			RetrievalMode:      "direct",
			IndexStatus:        "bypassed",
			CandidateCount:     1,
			SelectedCandidates: []SkillCandidate{directCandidate},
		}
	}
	querySet = normalizeSkillSearchQuerySet(querySet)
	if skillRetriever == nil || skillRetriever.EmbeddingProvider == nil {
		return retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, limit, "embedding_unavailable")
	}
	if errorValue := skillRetriever.refresh(ctx, skillInstructions); errorValue != nil {
		return retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, limit, "embedding_index_unavailable")
	}
	queryEmbeddings := skillRetriever.queryEmbeddings(ctx, querySet)
	if len(queryEmbeddings) == 0 {
		return retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, limit, "embedding_query_failed")
	}
	candidates, indexStatus := skillRetriever.embeddingCandidates(request, skillInstructions, queryEmbeddings, limit)
	if indexStatus != "ready" {
		return retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, limit, indexStatus)
	}
	return SkillRetrievalResult{
		RetrievalMode:      "embedding",
		IndexStatus:        indexStatus,
		CandidateCount:     len(candidates),
		QueryDescriptions:  skillSearchQueryDescriptions(querySet),
		SelectedCandidates: candidates,
	}
}

func retrieveSkillsWithBM25QuerySet(request AgentRequest, skillInstructions []SkillInstruction, querySet SkillSearchQuerySet, limit int, indexStatus string) SkillRetrievalResult {
	result := retrieveSkillsWithBM25(request, skillInstructions, skillSearchQueryText(querySet), limit, indexStatus)
	result.QueryDescriptions = skillSearchQueryDescriptions(querySet)
	return result
}

func (skillRetriever *EmbeddingSkillRetriever) Refresh(ctx context.Context, skillInstructions []SkillInstruction) {
	if skillRetriever == nil || skillRetriever.EmbeddingProvider == nil {
		return
	}
	_ = skillRetriever.refresh(ctx, skillInstructions)
}

func (skillRetriever *EmbeddingSkillRetriever) refresh(ctx context.Context, skillInstructions []SkillInstruction) error {
	skillRetriever.mutex.Lock()
	defer skillRetriever.mutex.Unlock()
	if !skillRetriever.isLoaded {
		skillRetriever.documents = skillRetriever.readIndex()
		skillRetriever.isLoaded = true
	}
	documentsByKey := skillSearchDocumentByKey(skillRetriever.documents)
	nextDocuments := []SkillSearchDocument{}
	for _, skillInstruction := range skillInstructions {
		if skillInstruction.DisableModelInvocation {
			continue
		}
		searchText := skillSearchText(skillInstruction)
		if strings.TrimSpace(searchText) == "" {
			continue
		}
		key := skillSearchDocumentKey(skillInstruction, skillRetriever.embeddingModel())
		document, isFound := documentsByKey[key]
		if isFound && document.SearchText == searchText && len(document.Embedding) > 0 {
			nextDocuments = append(nextDocuments, document)
			continue
		}
		embedding, errorValue := skillRetriever.EmbeddingProvider.GenerateEmbedding(ctx, searchText)
		if errorValue != nil {
			return errorValue
		}
		nextDocuments = append(nextDocuments, SkillSearchDocument{
			SkillName:      skillInstruction.Name,
			SourcePath:     skillInstruction.Source.Path,
			SourceSHA256:   skillInstruction.Source.SHA256,
			SearchText:     searchText,
			EmbeddingModel: skillRetriever.embeddingModel(),
			Embedding:      embedding,
			IndexedAt:      time.Now(),
		})
	}
	skillRetriever.documents = nextDocuments
	return skillRetriever.writeIndex(nextDocuments)
}

func (skillRetriever *EmbeddingSkillRetriever) queryEmbeddings(ctx context.Context, querySet SkillSearchQuerySet) [][]float32 {
	embeddings := [][]float32{}
	for _, query := range querySet.Queries {
		embedding, errorValue := skillRetriever.EmbeddingProvider.GenerateEmbedding(ctx, query.Description)
		if errorValue != nil || len(embedding) == 0 {
			continue
		}
		embeddings = append(embeddings, embedding)
	}
	return embeddings
}

func (skillRetriever *EmbeddingSkillRetriever) embeddingCandidates(request AgentRequest, skillInstructions []SkillInstruction, queryEmbeddings [][]float32, limit int) ([]SkillCandidate, string) {
	skillInstructionByName := skillInstructionByName(skillInstructions)
	candidates := []SkillCandidate{}
	hasDimensionMismatch := false
	for _, document := range skillRetriever.documents {
		skillInstruction, isFound := skillInstructionByName[document.SkillName]
		if !isFound || !isSkillAllowedForAutomaticRetrieval(skillInstruction, request) {
			continue
		}
		score, hasMatchedDimension := maximumCosineSimilarity(queryEmbeddings, document.Embedding)
		if !hasMatchedDimension {
			hasDimensionMismatch = true
			continue
		}
		if score <= 0 {
			continue
		}
		candidates = append(candidates, SkillCandidate{
			Name:   document.SkillName,
			Score:  score,
			Reason: "embedding_similarity",
			Source: skillInstruction.Source,
		})
	}
	if len(candidates) == 0 && hasDimensionMismatch {
		return nil, "embedding_dimension_mismatch"
	}
	sortSkillCandidates(candidates)
	return limitSkillCandidates(candidates, limit), "ready"
}

func (skillRetriever *EmbeddingSkillRetriever) readIndex() []SkillSearchDocument {
	if skillRetriever.IndexPath == "" {
		return nil
	}
	document, errorValue := os.ReadFile(skillRetriever.IndexPath)
	if errorValue != nil {
		return nil
	}
	searchDocuments := []SkillSearchDocument{}
	if errorValue := json.Unmarshal(document, &searchDocuments); errorValue != nil {
		return nil
	}
	return searchDocuments
}

func (skillRetriever *EmbeddingSkillRetriever) writeIndex(searchDocuments []SkillSearchDocument) error {
	if skillRetriever.IndexPath == "" {
		return nil
	}
	if errorValue := os.MkdirAll(filepath.Dir(skillRetriever.IndexPath), 0o755); errorValue != nil {
		return errorValue
	}
	document, errorValue := json.MarshalIndent(searchDocuments, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(skillRetriever.IndexPath, document, 0o644)
}

func (skillRetriever *EmbeddingSkillRetriever) embeddingModel() string {
	if strings.TrimSpace(skillRetriever.EmbeddingModel) != "" {
		return strings.TrimSpace(skillRetriever.EmbeddingModel) + ":" + skillSearchDocumentVersion
	}
	return defaultEmbeddingModelName + ":" + skillSearchDocumentVersion
}

func retrieveSkillsWithBM25(request AgentRequest, skillInstructions []SkillInstruction, queryText string, limit int, indexStatus string) SkillRetrievalResult {
	scores := rankSkillInstructionsByBM25(skillInstructions, nil, queryText)
	candidates := []SkillCandidate{}
	for _, score := range scores {
		skillInstruction, isFound := skillInstructionByName(skillInstructions)[score.Name]
		if !isFound || !isSkillAllowedForAutomaticRetrieval(skillInstruction, request) {
			continue
		}
		candidates = append(candidates, SkillCandidate{
			Name:   score.Name,
			Score:  score.Score,
			Reason: "bm25_fallback",
			Source: skillInstruction.Source,
		})
	}
	candidates = limitSkillCandidates(candidates, limit)
	return SkillRetrievalResult{
		RetrievalMode:      "bm25_fallback",
		IndexStatus:        firstNonEmptySkillSelectionString(indexStatus, "fallback"),
		CandidateCount:     len(candidates),
		QueryDescriptions:  skillSearchQueryDescriptions(SkillSearchQuerySet{Queries: []SkillSearchQuery{{Description: queryText}}}),
		SelectedCandidates: candidates,
	}
}

func directSkillCandidate(request AgentRequest, skillInstructions []SkillInstruction) (SkillCandidate, bool) {
	directSkillNames := directSkillNamesFromPrompt(request.Prompt)
	for _, skillInstruction := range skillInstructions {
		skillName := normalizeSkillSelectionText(skillInstruction.Name)
		if skillName == "" || !directSkillNames[skillName] {
			continue
		}
		if !isSkillAllowedForDirectRetrieval(skillInstruction, request) {
			continue
		}
		return SkillCandidate{
			Name:   skillInstruction.Name,
			Score:  1,
			Reason: "direct_skill_name",
			Source: skillInstruction.Source,
		}, true
	}
	return SkillCandidate{}, false
}

func directSkillNamesFromPrompt(prompt string) map[string]bool {
	directSkillNames := map[string]bool{}
	for _, field := range strings.Fields(prompt) {
		trimmedField := strings.Trim(strings.TrimSpace(field), ".,:;!?()[]{}\"'")
		if !strings.HasPrefix(trimmedField, "/") {
			continue
		}
		skillName := strings.TrimPrefix(trimmedField, "/")
		if skillName != "" {
			directSkillNames[normalizeSkillSelectionText(skillName)] = true
		}
	}
	return directSkillNames
}

func isSkillAllowedForDirectRetrieval(skillInstruction SkillInstruction, request AgentRequest) bool {
	return skillProfileAllows(skillInstruction, normalizedAgentProfileName(request.ProfileName)) && len(missingAllowedTools(skillInstruction, request)) == 0
}

func isSkillAllowedForAutomaticRetrieval(skillInstruction SkillInstruction, request AgentRequest) bool {
	return !skillInstruction.DisableModelInvocation && isSkillAllowedForDirectRetrieval(skillInstruction, request) && skillPathsAllow(skillInstruction, request)
}

func skillSearchDocumentByKey(searchDocuments []SkillSearchDocument) map[string]SkillSearchDocument {
	searchDocumentByKey := map[string]SkillSearchDocument{}
	for _, searchDocument := range searchDocuments {
		key := strings.Join([]string{searchDocument.SourcePath, searchDocument.SourceSHA256, searchDocument.EmbeddingModel}, "\x00")
		searchDocumentByKey[key] = searchDocument
	}
	return searchDocumentByKey
}

func skillSearchDocumentKey(skillInstruction SkillInstruction, embeddingModel string) string {
	return strings.Join([]string{skillInstruction.Source.Path, skillInstruction.Source.SHA256, embeddingModel}, "\x00")
}

func skillInstructionByName(skillInstructions []SkillInstruction) map[string]SkillInstruction {
	result := map[string]SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		result[skillInstruction.Name] = skillInstruction
	}
	return result
}

func sortSkillCandidates(candidates []SkillCandidate) {
	sort.SliceStable(candidates, func(leftIndex int, rightIndex int) bool {
		return candidates[leftIndex].Score > candidates[rightIndex].Score
	})
}

func limitSkillCandidates(candidates []SkillCandidate, limit int) []SkillCandidate {
	if limit <= 0 || len(candidates) <= limit {
		return candidates
	}
	return candidates[:limit]
}

func cosineSimilarity(left []float32, right []float32) float64 {
	if len(left) != len(right) {
		return 0
	}
	dotProduct := 0.0
	leftMagnitude := 0.0
	rightMagnitude := 0.0
	for index := range left {
		leftValue := float64(left[index])
		rightValue := float64(right[index])
		dotProduct += leftValue * rightValue
		leftMagnitude += leftValue * leftValue
		rightMagnitude += rightValue * rightValue
	}
	if leftMagnitude == 0 || rightMagnitude == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(leftMagnitude) * math.Sqrt(rightMagnitude))
}

func normalizeSkillSearchQuerySet(querySet SkillSearchQuerySet) SkillSearchQuerySet {
	queries := []SkillSearchQuery{}
	for _, query := range querySet.Queries {
		description := strings.TrimSpace(query.Description)
		if description == "" {
			continue
		}
		queries = append(queries, SkillSearchQuery{Description: description})
		if len(queries) >= maxGeneratedSkillSearchQueries {
			break
		}
	}
	return SkillSearchQuerySet{Queries: queries}
}

func skillSearchQueryText(querySet SkillSearchQuerySet) string {
	return strings.Join(skillSearchQueryDescriptions(querySet), "\n")
}

func skillSearchQueryDescriptions(querySet SkillSearchQuerySet) []string {
	descriptions := []string{}
	for _, query := range normalizeSkillSearchQuerySet(querySet).Queries {
		descriptions = append(descriptions, query.Description)
	}
	return descriptions
}

func maximumCosineSimilarity(queryEmbeddings [][]float32, documentEmbedding []float32) (float64, bool) {
	score := 0.0
	hasMatchedDimension := false
	for _, queryEmbedding := range queryEmbeddings {
		if len(queryEmbedding) != len(documentEmbedding) {
			continue
		}
		hasMatchedDimension = true
		score = math.Max(score, cosineSimilarity(queryEmbedding, documentEmbedding))
	}
	return score, hasMatchedDimension
}
