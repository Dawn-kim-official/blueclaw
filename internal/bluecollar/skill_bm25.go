package bluecollar

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const maxSkillIndexCandidateCount = 8
const maxSelectedSkillInstructionCount = 5
const minimumBM25SelectionScore = 0.25

type skillBM25Score struct {
	Name  string
	Score float64
}

type skillBM25Document struct {
	Name            string
	TokenCounts     map[string]int
	TokenTotalCount int
}

func rankSkillInstructionsByBM25(skillInstructions []SkillInstruction, skillDecisions []SkillSelectionDecision, prompt string) []skillBM25Score {
	candidateSkillInstructions := automaticCandidateSkillInstructions(skillInstructions, skillDecisions)
	if len(candidateSkillInstructions) == 0 {
		return nil
	}

	queryTokens := uniqueSkillSearchTokens(prompt)
	if len(queryTokens) == 0 {
		return nil
	}

	documents := buildSkillBM25Documents(candidateSkillInstructions)
	documentFrequencyByToken := skillDocumentFrequencyByToken(documents, queryTokens)
	averageTokenCount := averageSkillTokenCount(documents)
	scores := []skillBM25Score{}
	for _, document := range documents {
		score := calculateSkillBM25Score(document, queryTokens, documentFrequencyByToken, len(documents), averageTokenCount)
		score += exactSkillNameMatchBoost(document.Name, prompt)
		if score > 0 {
			scores = append(scores, skillBM25Score{Name: document.Name, Score: score})
		}
	}
	sort.SliceStable(scores, func(leftIndex int, rightIndex int) bool {
		return scores[leftIndex].Score > scores[rightIndex].Score
	})
	return scores
}

func automaticCandidateSkillInstructions(skillInstructions []SkillInstruction, skillDecisions []SkillSelectionDecision) []SkillInstruction {
	decisionByName := map[string]SkillSelectionDecision{}
	for _, skillDecision := range skillDecisions {
		decisionByName[skillDecision.Name] = skillDecision
	}
	candidates := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		skillDecision, isFound := decisionByName[skillInstruction.Name]
		if !isFound || skillDecision.Status == "selected" || skillDecision.Reason == "no_trigger_matched" {
			candidates = append(candidates, skillInstruction)
		}
	}
	return candidates
}

func buildSkillBM25Documents(skillInstructions []SkillInstruction) []skillBM25Document {
	documents := []skillBM25Document{}
	for _, skillInstruction := range skillInstructions {
		tokens := skillSearchTokens(skillSearchText(skillInstruction))
		if len(tokens) == 0 {
			continue
		}
		documents = append(documents, skillBM25Document{
			Name:            skillInstruction.Name,
			TokenCounts:     countSkillSearchTokens(tokens),
			TokenTotalCount: len(tokens),
		})
	}
	return documents
}

func calculateSkillBM25Score(document skillBM25Document, queryTokens []string, documentFrequencyByToken map[string]int, documentCount int, averageTokenCount float64) float64 {
	score := 0.0
	for _, token := range queryTokens {
		termFrequency := document.TokenCounts[token]
		if termFrequency == 0 {
			continue
		}
		score += calculateSkillBM25TermScore(termFrequency, document.TokenTotalCount, documentFrequencyByToken[token], documentCount, averageTokenCount)
	}
	return score
}

func calculateSkillBM25TermScore(termFrequency int, documentTokenCount int, documentFrequency int, documentCount int, averageTokenCount float64) float64 {
	if averageTokenCount == 0 {
		return 0
	}
	termSaturation := 1.2
	lengthNormalization := 0.75
	inverseDocumentFrequency := math.Log(1 + (float64(documentCount-documentFrequency)+0.5)/(float64(documentFrequency)+0.5))
	normalizedLength := float64(documentTokenCount) / averageTokenCount
	denominator := float64(termFrequency) + termSaturation*(1-lengthNormalization+lengthNormalization*normalizedLength)
	return inverseDocumentFrequency * (float64(termFrequency) * (termSaturation + 1)) / denominator
}

func skillDocumentFrequencyByToken(documents []skillBM25Document, queryTokens []string) map[string]int {
	documentFrequencyByToken := map[string]int{}
	for _, token := range queryTokens {
		for _, document := range documents {
			if document.TokenCounts[token] > 0 {
				documentFrequencyByToken[token]++
			}
		}
	}
	return documentFrequencyByToken
}

func averageSkillTokenCount(documents []skillBM25Document) float64 {
	if len(documents) == 0 {
		return 0
	}
	totalTokenCount := 0
	for _, document := range documents {
		totalTokenCount += document.TokenTotalCount
	}
	return float64(totalTokenCount) / float64(len(documents))
}

func skillSearchText(skillInstruction SkillInstruction) string {
	parts := []string{
		skillInstruction.Description,
		skillInstruction.WhenToUse,
		skillInstruction.Category,
		strings.Join(skillInstruction.Tags, " "),
		strings.Join(skillInstruction.TriggerHints, " "),
		strings.Join(skillInstruction.ToolReferences, " "),
	}
	text := strings.Join(nonEmptyStrings(parts), " ")
	if text != "" {
		return text
	}
	return strings.TrimSpace(skillInstruction.Name)
}

func skillSearchTokens(value string) []string {
	tokens := []string{}
	for _, token := range strings.Fields(normalizeSkillSearchText(value)) {
		if shouldKeepSkillSearchToken(token) {
			tokens = append(tokens, token)
			tokens = append(tokens, nonASCIICharacterBigrams(token)...)
		}
	}
	return tokens
}

func nonASCIICharacterBigrams(token string) []string {
	characters := []rune(token)
	if len(characters) < 3 {
		return nil
	}
	hasNonASCII := false
	for _, character := range characters {
		if character > unicode.MaxASCII {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII {
		return nil
	}
	bigrams := make([]string, 0, len(characters)-1)
	for index := 0; index < len(characters)-1; index++ {
		bigrams = append(bigrams, string(characters[index:index+2]))
	}
	return bigrams
}

func uniqueSkillSearchTokens(value string) []string {
	seenTokens := map[string]bool{}
	tokens := []string{}
	for _, token := range skillSearchTokens(value) {
		if seenTokens[token] {
			continue
		}
		seenTokens[token] = true
		tokens = append(tokens, token)
	}
	return tokens
}

func countSkillSearchTokens(tokens []string) map[string]int {
	tokenCounts := map[string]int{}
	for _, token := range tokens {
		tokenCounts[token]++
	}
	return tokenCounts
}

func normalizeSkillSearchText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return ' '
	}, value)
}

func shouldKeepSkillSearchToken(token string) bool {
	if len([]rune(token)) <= 1 {
		return false
	}
	return !skillSearchStopWords[token]
}

func exactSkillNameMatchBoost(skillName string, prompt string) float64 {
	normalizedSkillName := normalizeSkillSelectionText(skillName)
	if normalizedSkillName == "" {
		return 0
	}
	for _, token := range strings.Fields(normalizeSkillSelectionText(prompt)) {
		if strings.Trim(token, "\t\n\r.,:;!?()[]{}<>") == "/"+normalizedSkillName {
			return 1000
		}
	}
	return 0
}

var skillSearchStopWords = map[string]bool{
	"and": true, "are": true, "for": true, "the": true, "this": true, "that": true,
	"use": true, "when": true, "with": true, "사용": true, "요청": true,
}
