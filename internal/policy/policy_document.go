package policy

type PolicyDocument struct {
	People    []PersonPolicy  `json:"people"`
	Channels  []ChannelPolicy `json:"channels"`
	Retention RetentionPolicy `json:"retention"`
	Rules     []TopicRule     `json:"rules"`
	Metadata  PolicyMetadata  `json:"metadata"`
}

type PersonPolicy struct {
	PersonID          string   `json:"personID"`
	DisplayName       string   `json:"displayName"`
	Emails            []string `json:"emails"`
	SecurityLevelName string   `json:"securityLevelName"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	GrantedClasses    []string `json:"grantedClasses"`
	IsAdmin           bool     `json:"isAdmin"`
}

type ChannelPolicy struct {
	Platform                 string   `json:"platform"`
	ExternalConversationID   string   `json:"externalConversationID"`
	ConversationType         string   `json:"conversationType"`
	DisplayName              string   `json:"displayName"`
	DefaultSecurityLevelRank int      `json:"defaultSecurityLevelRank"`
	DefaultRequiredClasses   []string `json:"defaultRequiredClasses"`
	IsCollectEnabled         bool     `json:"isCollectEnabled"`
	IsReplyEnabled           bool     `json:"isReplyEnabled"`
}

type RetentionPolicy struct {
	RawEventDays int `json:"rawEventDays"`
}

type TopicRule struct {
	Name                string   `json:"name"`
	TopicKeywords       []string `json:"topicKeywords"`
	RequiredClasses     []string `json:"requiredClasses"`
	MinimumSecurityRank int      `json:"minimumSecurityRank"`
}

type PolicyMetadata struct {
	Version int    `json:"version"`
	Author  string `json:"author"`
}
