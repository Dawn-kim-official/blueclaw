package policy

import "strings"

type PolicyProjection struct {
	ApprovedEmailByPersonID map[string][]string
	PersonIDByEmail         map[string]string
	PersonAccessByPersonID  map[string]PersonAccess
	ChannelByCompositeKey   map[string]ChannelPolicy
}

type PersonAccess struct {
	PersonID          string
	SecurityLevelRank int
	GrantedClasses    []string
}

type PolicyProjectionService struct{}

func (policyProjectionService PolicyProjectionService) ReplacePolicyProjectionTransactionally(policyDocument PolicyDocument) PolicyProjection {
	policyProjection := PolicyProjection{
		ApprovedEmailByPersonID: map[string][]string{},
		PersonIDByEmail:         map[string]string{},
		PersonAccessByPersonID:  map[string]PersonAccess{},
		ChannelByCompositeKey:   map[string]ChannelPolicy{},
	}

	for _, personPolicy := range policyDocument.People {
		policyProjection.ApprovedEmailByPersonID[personPolicy.PersonID] = append([]string{}, personPolicy.Emails...)
		policyProjection.PersonAccessByPersonID[personPolicy.PersonID] = PersonAccess{
			PersonID:          personPolicy.PersonID,
			SecurityLevelRank: personPolicy.SecurityLevelRank,
			GrantedClasses:    append([]string{}, personPolicy.GrantedClasses...),
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
