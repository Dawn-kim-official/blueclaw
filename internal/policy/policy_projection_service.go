package policy

import "strings"

type PolicyProjection struct {
	ApprovedEmailByPersonID map[string][]string
	PersonIDByEmail         map[string]string
	ChannelByCompositeKey   map[string]ChannelPolicy
}

type PolicyProjectionService struct{}

func (policyProjectionService PolicyProjectionService) ReplacePolicyProjectionTransactionally(policyDocument PolicyDocument) PolicyProjection {
	policyProjection := PolicyProjection{
		ApprovedEmailByPersonID: map[string][]string{},
		PersonIDByEmail:         map[string]string{},
		ChannelByCompositeKey:   map[string]ChannelPolicy{},
	}

	for _, personPolicy := range policyDocument.People {
		policyProjection.ApprovedEmailByPersonID[personPolicy.PersonID] = append([]string{}, personPolicy.Emails...)
		for _, email := range personPolicy.Emails {
			policyProjection.PersonIDByEmail[strings.ToLower(email)] = personPolicy.PersonID
		}
	}

	for _, channelPolicy := range policyDocument.Channels {
		policyProjection.ChannelByCompositeKey[channelPolicy.Platform+":"+channelPolicy.ExternalConversationID] = channelPolicy
	}

	return policyProjection
}
