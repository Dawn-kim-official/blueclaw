package bluecollar

import (
	"errors"
	"strconv"
	"strings"
)

type QualityState struct {
	Passed   bool                `json:"passed"`
	Criteria []qualityCriterion  `json:"criteria,omitempty"`
	Review   []qualityReviewItem `json:"review,omitempty"`
}

func normalizeQualityCriteria(criteria []string) []qualityCriterion {
	normalizedCriteria := []qualityCriterion{}
	seenCriterion := map[string]bool{}
	for index, criterion := range criteria {
		normalizedCriterion := normalizeQualityCriterion(criterion, index+1)
		if normalizedCriterion.ID == "" || seenCriterion[normalizedCriterion.ID] {
			continue
		}
		seenCriterion[normalizedCriterion.ID] = true
		normalizedCriteria = append(normalizedCriteria, normalizedCriterion)
	}
	return normalizedCriteria
}

func normalizeQualityCriterion(criterion string, index int) qualityCriterion {
	description := strings.TrimSpace(criterion)
	id := normalizedQualityID(description)
	if id == "" {
		id = "criterion-" + strconv.Itoa(index)
	}
	if description == "" {
		description = id
	}
	return qualityCriterion{
		ID:          id,
		Description: description,
		Required:    true,
	}
}

func validateQualityReview(criteria []qualityCriterion, review []qualityReviewItem, observations []turnObservation) error {
	if len(criteria) == 0 {
		return nil
	}
	reviewByID := qualityReviewByID(review)
	for _, criterion := range criteria {
		if !criterion.Required {
			continue
		}
		item, isFound := reviewByID[criterion.ID]
		if !isFound {
			return errors.New("finish qualityReview must include required criterion " + criterion.ID)
		}
		if !item.Passed {
			return errors.New("finish qualityReview did not pass required criterion " + criterion.ID)
		}
		if len(item.Evidence) == 0 {
			return errors.New("finish qualityReview for " + criterion.ID + " must cite evidence")
		}
		if errorValue := validateQualityEvidenceReferences(item.Evidence, observations); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func qualityReviewByID(review []qualityReviewItem) map[string]qualityReviewItem {
	reviewByID := map[string]qualityReviewItem{}
	for _, item := range review {
		id := normalizedQualityID(item.ID)
		if id == "" {
			continue
		}
		item.ID = id
		reviewByID[id] = item
	}
	return reviewByID
}

func validateQualityEvidenceReferences(references []completionEvidenceReference, observations []turnObservation) error {
	for _, reference := range references {
		if _, isFound := findSuccessfulObservation(observations, reference); !isFound {
			return errors.New("qualityReview evidence references an unknown or failed observation")
		}
	}
	return nil
}

func normalizedQualityID(value string) string {
	trimmedValue := strings.ToLower(strings.TrimSpace(value))
	builder := strings.Builder{}
	previousDash := false
	for _, character := range trimmedValue {
		isAllowed := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if isAllowed {
			builder.WriteRune(character)
			previousDash = false
			continue
		}
		if !previousDash {
			builder.WriteRune('-')
			previousDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func buildQualityState(criteria []qualityCriterion, review []qualityReviewItem, observations []turnObservation) QualityState {
	return QualityState{
		Passed:   validateQualityReview(criteria, review, observations) == nil,
		Criteria: criteria,
		Review:   review,
	}
}
