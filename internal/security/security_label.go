package security

type SecurityLabel struct {
	SourceConversationID     string   `json:"sourceConversationID"`
	MinimumSecurityLevelRank int      `json:"minimumSecurityLevelRank"`
	RequiredClasses          []string `json:"requiredClasses"`
}
