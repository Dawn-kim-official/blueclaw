package policy

import "strings"

type PolicyProjection struct {
	ApprovedEmailByPersonID map[string][]string
	PersonIDByEmail         map[string]string
	PersonAccessByPersonID  map[string]PersonAccess
	ChannelByCompositeKey   map[string]ChannelPolicy
	ResourceAccessRules     []ResourceAccessPolicy
}

type PersonAccess struct {
	PersonID            string
	Circles             []string
	ResourceAccessRules []ResourceAccessPolicy
	SecurityLevelRank   int
	GrantedClasses      []string
}

type PolicyProjectionService struct{}

func (policyProjectionService PolicyProjectionService) ReplacePolicyProjectionTransactionally(policyDocument PolicyDocument) PolicyProjection {
	policyProjection := PolicyProjection{
		ApprovedEmailByPersonID: map[string][]string{},
		PersonIDByEmail:         map[string]string{},
		PersonAccessByPersonID:  map[string]PersonAccess{},
		ChannelByCompositeKey:   map[string]ChannelPolicy{},
		ResourceAccessRules:     append([]ResourceAccessPolicy{}, policyDocument.ResourceAccess...),
	}

	for _, personPolicy := range policyDocument.People {
		policyProjection.ApprovedEmailByPersonID[personPolicy.PersonID] = append([]string{}, personPolicy.Emails...)
		policyProjection.PersonAccessByPersonID[personPolicy.PersonID] = PersonAccess{
			PersonID:            personPolicy.PersonID,
			Circles:             effectivePersonCircles(personPolicy),
			ResourceAccessRules: append([]ResourceAccessPolicy{}, policyDocument.ResourceAccess...),
			SecurityLevelRank:   personPolicy.SecurityLevelRank,
			GrantedClasses:      append([]string{}, personPolicy.GrantedClasses...),
		}
		for _, email := range personPolicy.Emails {
			policyProjection.PersonIDByEmail[strings.ToLower(email)] = personPolicy.PersonID
		}
	}

	for _, channelPolicy := range policyDocument.Channels {
		policyProjection.ChannelByCompositeKey[channelPolicy.Platform+":"+channelPolicy.ExternalConversationID] = channelPolicy
	}

	return policyProjection
}

func effectivePersonCircles(personPolicy PersonPolicy) []string {
	circles := append([]string{"staff"}, personPolicy.Circles...)
	if personPolicy.IsAdmin {
		circles = append(circles, "admin")
	}
	return normalizePolicyStrings(circles)
}

func normalizePolicyStrings(values []string) []string {
	seenValue := map[string]bool{}
	normalizedValues := []string{}
	for _, value := range values {
		normalizedValue := strings.ToLower(strings.TrimSpace(value))
		if normalizedValue == "" || seenValue[normalizedValue] {
			continue
		}
		seenValue[normalizedValue] = true
		normalizedValues = append(normalizedValues, normalizedValue)
	}
	return normalizedValues
}
