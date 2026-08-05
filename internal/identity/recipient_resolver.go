package identity

import (
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
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
	matches := exactRecipientMatches(normalizedHint, candidates)
	switch len(matches) {
	case 0:
		return RecipientResolution{Status: RecipientNotFound, ApprovedPeople: approvedPeopleNames(people)}
	case 1:
		return singleRecipientResolution(matches[0])
	default:
		return RecipientResolution{Status: RecipientAmbiguous, Candidates: matches}
	}
}

func singleRecipientResolution(recipient RecipientCandidate) RecipientResolution {
	if recipient.ExternalUserID == "" {
		return RecipientResolution{Status: RecipientUnlinked, Recipient: &recipient}
	}
	return RecipientResolution{Status: RecipientResolved, Recipient: &recipient}
}

type recipientMatchCandidate struct {
	candidate   RecipientCandidate
	matchValues []string
}

func recipientCandidates(platform string, people []policy.PersonPolicy, platformAccounts []PlatformAccountIdentity) []recipientMatchCandidate {
	accountsByPersonID := platformAccountsByPersonID(platform, people, platformAccounts)
	candidates := []recipientMatchCandidate{}
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
		candidates = append(candidates, recipientMatchCandidate{candidate: candidate, matchValues: matchValues})
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

func exactRecipientMatches(normalizedHint string, candidates []recipientMatchCandidate) []RecipientCandidate {
	matches := []RecipientCandidate{}
	for _, candidate := range candidates {
		if recipientMatchesHint(normalizedHint, candidate.matchValues) {
			matches = append(matches, candidate.candidate)
		}
	}
	sort.Slice(matches, func(first int, second int) bool {
		return matches[first].DisplayName < matches[second].DisplayName
	})
	return matches
}

func recipientMatchesHint(normalizedHint string, matchValues []string) bool {
	for _, value := range matchValues {
		if normalizeRecipientMatchValue(value) == normalizedHint {
			return true
		}
	}
	return false
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
