package identity

type PlatformAccountIdentity struct {
	Platform       string `json:"platform"`
	ExternalUserID string `json:"externalUserID"`
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	PersonID       string `json:"personID"`
}
