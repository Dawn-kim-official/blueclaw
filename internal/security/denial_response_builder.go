package security

type DenialResponseBuilder struct{}

func (denialResponseBuilder DenialResponseBuilder) BuildDeniedReply() string {
	return "That request is not available in your current access context."
}
