package agent

import (
	"errors"
	"regexp"
	"strings"
)

var finalReplyAttachmentDeliveryClaimExpressions = compileRegularExpressions(finalReplyAttachmentDeliveryClaimPatterns())

func ValidateFinalReplyDelivery(reply string, attachments []FileAttachment, requiresArtifactEvidence bool) error {
	if locator := finalReplyNonDeliverableArtifactLocator(reply); locator != "" {
		return errors.New("final_reply exposes non-deliverable artifact locator " + locator + "; cite attached filenames from completionEvidence only")
	}
	if !requiresArtifactEvidence && len(attachments) == 0 {
		return nil
	}
	if filename := finalReplyUnattachedArtifactFilename(reply, attachments); filename != "" {
		return errors.New("final_reply mentions artifact filename " + filename + " without matching completionEvidence attachment")
	}
	return nil
}

func FinalReplyClaimsAttachmentDelivery(reply string) bool {
	normalizedReply := strings.ToLower(strings.TrimSpace(reply))
	if normalizedReply == "" {
		return false
	}
	for _, expression := range finalReplyAttachmentDeliveryClaimExpressions {
		if expression.MatchString(normalizedReply) {
			return true
		}
	}
	return false
}

func FinalReplyContainsNonDeliverableArtifactLocator(reply string) bool {
	return finalReplyNonDeliverableArtifactLocator(reply) != ""
}

func finalReplyAttachmentDeliveryClaimPatterns() []string {
	return []string{
		`(?i)\b(?:i'?ve|i have|we'?ve|we have)\s+(?:attached|sent|delivered)\b`,
		`(?i)\b(?:attached|sent|delivered)\s+(?:the\s+)?(?:file|files|attachment|attachments|html|pptx|pdf|deck|slides|notes)\b`,
		`(?i)\b(?:file|files|attachment|attachments|html|pptx|pdf|deck|slides|notes)\s+(?:is|are|has been|have been)\s+(?:attached|sent|delivered)\b`,
		`첨부(?:했|했습|했어|했습니다|했어요|해드렸|해 드렸|되어 있|된)`,
		`(?:파일|자료|html|pptx|pdf|deck|slide|슬라이드).*(?:전달해 드립니다|전달했습니다|전달했어요|보내드렸습니다|보내드렸어요|보냈습니다|보냈어요|확인해 주세요)`,
		`첨부된\s*(?:파일|자료)`,
	}
}

func compileRegularExpressions(patterns []string) []*regexp.Regexp {
	expressions := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		expressions = append(expressions, regexp.MustCompile(pattern))
	}
	return expressions
}

func finalReplyNonDeliverableArtifactLocator(reply string) string {
	normalizedReply := strings.TrimSpace(reply)
	for _, pattern := range finalReplyNonDeliverableLocatorPatterns() {
		if locator := strings.TrimSpace(regexp.MustCompile(pattern).FindString(normalizedReply)); locator != "" {
			return strings.Trim(locator, " ([")
		}
	}
	return ""
}

func finalReplyNonDeliverableLocatorPatterns() []string {
	return []string{
		`(?i)\bsandbox:/[^\s\])>"']+`,
		`(?i)\bfile:/+[^\s\])>"']+`,
		`(?i)(?:^|[\s\[(])/(?:mnt/data|tmp|root|workspace|var/folders)/[^\s\])>"']+`,
	}
}

func finalReplyUnattachedArtifactFilename(reply string, attachments []FileAttachment) string {
	attachedFilenames := attachedFilenameSet(attachments)
	for _, filename := range finalReplyArtifactFilenames(reply) {
		if !attachedFilenames[strings.ToLower(filename)] {
			return filename
		}
	}
	return ""
}

func attachedFilenameSet(attachments []FileAttachment) map[string]bool {
	filenames := map[string]bool{}
	for _, attachment := range attachments {
		filename := strings.ToLower(strings.TrimSpace(attachment.Filename))
		if filename != "" {
			filenames[filename] = true
		}
	}
	return filenames
}

func finalReplyArtifactFilenames(reply string) []string {
	matches := regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}._ -]*\.(?:html|pptx|pdf|txt|md|csv|xlsx|docx)`).FindAllString(reply, -1)
	filenames := []string{}
	for _, match := range matches {
		filename := strings.Trim(strings.TrimSpace(match), ".,;:)]}\"'")
		if filename != "" {
			filenames = append(filenames, filename)
		}
	}
	return filenames
}
