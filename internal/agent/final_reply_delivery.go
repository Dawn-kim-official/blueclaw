package agent

import (
	"errors"
	"regexp"
	"strings"
)

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
	if normalizedReply == "" || containsAny(normalizedReply, attachmentDeliveryNegations()) {
		return false
	}
	if containsAny(normalizedReply, []string{"attached", "attachment"}) {
		return true
	}
	if containsAny(normalizedReply, []string{"첨부", "전달", "보내드", "보냈"}) &&
		containsAny(normalizedReply, []string{"파일", "pptx", "pdf", "html", "notes", "자료", "deck", "slide"}) {
		return true
	}
	return containsAny(normalizedReply, []string{"첨부된 파일", "파일들을 확인", "파일을 확인", "attached files"})
}

func FinalReplyContainsNonDeliverableArtifactLocator(reply string) bool {
	return finalReplyNonDeliverableArtifactLocator(reply) != ""
}

func attachmentDeliveryNegations() []string {
	return []string{
		"not attached", "not attach", "cannot attach", "could not attach", "failed to attach",
		"첨부하지 못", "첨부할 수 없", "첨부 실패", "파일을 전달하지 못", "파일로 전달하지 못",
	}
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
