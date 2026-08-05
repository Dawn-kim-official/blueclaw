package security

import "github.com/yeomyeonggeori/blueclaw/internal/policy"

type SecurityLabeler struct{}

func (securityLabeler SecurityLabeler) ClassifyChannelPolicy(channelPolicy policy.ChannelPolicy) SecurityLabel {
	return SecurityLabel{
		SourceConversationID:     channelPolicy.ExternalConversationID,
		MinimumSecurityLevelRank: channelPolicy.DefaultSecurityLevelRank,
		RequiredClasses:          append([]string{}, channelPolicy.DefaultRequiredClasses...),
	}
}
