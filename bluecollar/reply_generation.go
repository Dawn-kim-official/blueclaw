package bluecollar

import (
	"strings"
)

func buildSenderAddressingDescription(request AgentTurnRequest) string {
	callingName := strings.TrimSpace(request.RequesterCallingName)
	fullName := strings.TrimSpace(request.RequesterName)
	handle := strings.TrimSpace(request.RequesterHandle)
	if callingName == "" {
		callingName = fullName
	}
	if callingName == "" && handle == "" {
		return ""
	}

	descriptionLines := []string{"You are speaking with the following user:"}
	if fullName != "" {
		descriptionLines = append(descriptionLines, "- Full name: "+fullName)
	}
	if callingName != "" {
		descriptionLines = append(descriptionLines, "- Calling name: "+callingName)
	}
	if handle != "" {
		descriptionLines = append(descriptionLines, "- Handle: @"+handle)
	}
	descriptionLines = append(descriptionLines,
		"When addressing them in Korean, call them \""+callingName+" 님\".",
		"When addressing them in English, call them \""+callingName+"\".",
		"If multiple participants in this conversation share the same calling name, append \"@handle\" when addressing them to disambiguate.",
	)
	return strings.Join(descriptionLines, "\n")
}
