package identity

import (
	"sort"
	"strings"

	"blueclaw/internal/policy"
)

type RecipientResolutionStatus string

const (
	RecipientResolved  RecipientResolutionStatus = "resolved"
	RecipientAmbiguous RecipientResolutionStatus = "ambiguous"
	RecipientUnlinked  RecipientResolutionStatus = "unlinked"
	RecipientNotFound  RecipientResolutionStatus = "not_found"
)

type RecipientCandidate struct {
	PersonID       string   `json:"personID"`
	DisplayName    string   `json:"displayName"`
	Emails         []string `json:"emails,omitempty"`
	ExternalUserID string   `json:"externalUserID,omitempty"`
	Username       string   `json:"username,omitempty"`
}

type RecipientResolution struct {
	Status         RecipientResolutionStatus `json:"status"`
	Recipient      *RecipientCandidate       `json:"recipient,omitempty"`
	Candidates     []RecipientCandidate      `json:"candidates,omitempty"`
	ApprovedPeople []string                  `json:"approvedPeople,omitempty"`
}

func ResolveRecipient(platform string, hint string, people []policy.PersonPolicy, platformAccounts []PlatformAccountIdentity) RecipientResolution {
	normalizedHint := normalizeRecipientMatchValue(hint)
	if normalizedHint == "" {
		return RecipientResolution{Status: RecipientNotFound, ApprovedPeople: approvedPeopleNames(people)}
	}
	candidates := recipientCandidates(platform, people, platformAccounts)
	matches := bestScoredRecipientMatches(normalizedHint, candidates)
	if len(matches) == 0 {
		matches = fuzzyRecipientMatches(normalizedHint, candidates)
	}
	switch len(matches) {
	case 0:
		return RecipientResolution{Status: RecipientNotFound, ApprovedPeople: approvedPeopleNames(people)}
	case 1:
		return singleRecipientResolution(matches[0])
	default:
		return RecipientResolution{Status: RecipientAmbiguous, Candidates: matches}
	}
}

func fuzzyRecipientMatches(normalizedHint string, candidates []scoredRecipientCandidate) []RecipientCandidate {
	matches := []RecipientCandidate{}
	bestOverlap := 0
	for _, candidate := range candidates {
		overlap := fuzzyCandidateOverlap(normalizedHint, candidate.matchValues)
		if overlap == 0 {
			continue
		}
		if overlap > bestOverlap {
			bestOverlap = overlap
			matches = nil
		}
		if overlap == bestOverlap {
			matches = append(matches, candidate.candidate)
		}
	}
	sort.Slice(matches, func(first int, second int) bool {
		return matches[first].DisplayName < matches[second].DisplayName
	})
	return matches
}

func fuzzyCandidateOverlap(normalizedHint string, matchValues []string) int {
	best := 0
	for _, value := range matchValues {
		normalizedValue := normalizeRecipientMatchValue(value)
		if normalizedValue == "" {
			continue
		}
		overlap := longestCommonSubstringLength([]rune(normalizedHint), []rune(normalizedValue))
		shorter := len([]rune(normalizedHint))
		if valueLength := len([]rune(normalizedValue)); valueLength < shorter {
			shorter = valueLength
		}
		if overlap < 2 || overlap*3 < shorter*2 {
			continue
		}
		if overlap > best {
			best = overlap
		}
	}
	return best
}

func longestCommonSubstringLength(left []rune, right []rune) int {
	longest := 0
	previousRow := make([]int, len(right)+1)
	for leftIndex := 1; leftIndex <= len(left); leftIndex++ {
		currentRow := make([]int, len(right)+1)
		for rightIndex := 1; rightIndex <= len(right); rightIndex++ {
			if left[leftIndex-1] == right[rightIndex-1] {
				currentRow[rightIndex] = previousRow[rightIndex-1] + 1
				if currentRow[rightIndex] > longest {
					longest = currentRow[rightIndex]
				}
			}
		}
		previousRow = currentRow
	}
	return longest
}

func singleRecipientResolution(recipient RecipientCandidate) RecipientResolution {
	if recipient.ExternalUserID == "" {
		return RecipientResolution{Status: RecipientUnlinked, Recipient: &recipient}
	}
	return RecipientResolution{Status: RecipientResolved, Recipient: &recipient}
}

type scoredRecipientCandidate struct {
	candidate   RecipientCandidate
	matchValues []string
}

func recipientCandidates(platform string, people []policy.PersonPolicy, platformAccounts []PlatformAccountIdentity) []scoredRecipientCandidate {
	accountsByPersonID := platformAccountsByPersonID(platform, people, platformAccounts)
	candidates := []scoredRecipientCandidate{}
	for _, person := range people {
		candidate := RecipientCandidate{
			PersonID:    strings.TrimSpace(person.PersonID),
			DisplayName: strings.TrimSpace(person.DisplayName),
			Emails:      normalizedRecipientEmails(person.Emails),
		}
		matchValues := append([]string{candidate.PersonID, candidate.DisplayName}, candidate.Emails...)
		for _, account := range accountsByPersonID[candidate.PersonID] {
			candidate.ExternalUserID = account.ExternalUserID
			candidate.Username = strings.TrimSpace(account.DisplayName)
			matchValues = append(matchValues, account.DisplayName, account.Email)
		}
		candidates = append(candidates, scoredRecipientCandidate{candidate: candidate, matchValues: matchValues})
	}
	return candidates
}

func platformAccountsByPersonID(platform string, people []policy.PersonPolicy, platformAccounts []PlatformAccountIdentity) map[string][]PlatformAccountIdentity {
	personIDByEmail := map[string]string{}
	for _, person := range people {
		for _, email := range person.Emails {
			personIDByEmail[normalizeRecipientMatchValue(email)] = strings.TrimSpace(person.PersonID)
		}
	}
	accountsByPersonID := map[string][]PlatformAccountIdentity{}
	for _, account := range platformAccounts {
		if !strings.EqualFold(strings.TrimSpace(account.Platform), strings.TrimSpace(platform)) {
			continue
		}
		personID := personIDByEmail[normalizeRecipientMatchValue(account.Email)]
		if personID == "" {
			personID = strings.TrimSpace(account.PersonID)
		}
		if personID == "" {
			continue
		}
		accountsByPersonID[personID] = append(accountsByPersonID[personID], account)
	}
	return accountsByPersonID
}

func bestScoredRecipientMatches(normalizedHint string, candidates []scoredRecipientCandidate) []RecipientCandidate {
	matches := []RecipientCandidate{}
	bestScore := 0
	for _, candidate := range candidates {
		score := recipientMatchScore(normalizedHint, candidate.matchValues)
		if score == 0 {
			continue
		}
		if bestScore == 0 || score < bestScore {
			bestScore = score
			matches = nil
		}
		if score == bestScore {
			matches = append(matches, candidate.candidate)
		}
	}
	sort.Slice(matches, func(first int, second int) bool {
		return matches[first].DisplayName < matches[second].DisplayName
	})
	return matches
}

func recipientMatchScore(normalizedHint string, matchValues []string) int {
	for _, value := range matchValues {
		if normalizeRecipientMatchValue(value) == normalizedHint {
			return 1
		}
	}
	for _, value := range matchValues {
		normalizedValue := normalizeRecipientMatchValue(value)
		if normalizedValue == "" {
			continue
		}
		if strings.Contains(normalizedValue, normalizedHint) || strings.Contains(normalizedHint, normalizedValue) {
			return 2
		}
	}
	return 0
}

func normalizeRecipientMatchValue(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "@")))), "")
}

func normalizedRecipientEmails(emails []string) []string {
	normalizedEmails := []string{}
	seenEmail := map[string]bool{}
	for _, email := range emails {
		normalizedEmail := normalizeRecipientMatchValue(email)
		if normalizedEmail == "" || seenEmail[normalizedEmail] {
			continue
		}
		seenEmail[normalizedEmail] = true
		normalizedEmails = append(normalizedEmails, normalizedEmail)
	}
	return normalizedEmails
}

func approvedPeopleNames(people []policy.PersonPolicy) []string {
	names := []string{}
	for _, person := range people {
		name := strings.TrimSpace(person.DisplayName)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
