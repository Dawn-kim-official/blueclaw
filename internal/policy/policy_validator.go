package policy

import (
	"errors"
	"strings"
)

type PolicyValidator struct{}

func (policyValidator PolicyValidator) ValidatePolicyDocument(policyDocument PolicyDocument) error {
	emailSet := map[string]bool{}
	personSet := map[string]bool{}

	for _, personPolicy := range policyDocument.People {
		if personPolicy.PersonID == "" {
			return errors.New("personID is required")
		}
		if personSet[personPolicy.PersonID] {
			return errors.New("duplicate personID")
		}
		personSet[personPolicy.PersonID] = true
		if len(personPolicy.Emails) == 0 {
			return errors.New("person email is required")
		}
		for _, email := range personPolicy.Emails {
			normalizedEmail := strings.ToLower(strings.TrimSpace(email))
			if normalizedEmail == "" {
				return errors.New("person email is required")
			}
			if emailSet[normalizedEmail] {
				return errors.New("duplicate email")
			}
			emailSet[normalizedEmail] = true
		}
	}

	for _, channelPolicy := range policyDocument.Channels {
		if channelPolicy.Platform == "" || channelPolicy.ExternalConversationID == "" {
			return errors.New("channel platform and externalConversationID are required")
		}
	}

	if policyDocument.Retention.RawEventDays <= 0 {
		return errors.New("retention rawEventDays must be positive")
	}

	return nil
}
