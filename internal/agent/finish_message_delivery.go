package agent

import (
	"errors"
	"regexp"
	"strings"
)

func ValidateFinishMessageDelivery(reply string, attachments []FileAttachment, requiresArtifactEvidence bool) error {
	if locator := finishMessageNonDeliverableArtifactLocator(reply); locator != "" {
		return errors.New("finish exposes non-deliverable artifact locator " + locator + "; cite attached filenames from completionEvidence only")
	}
	if !requiresArtifactEvidence && len(attachments) == 0 {
		return nil
	}
	if filename := finishMessageUnattachedArtifactFilename(reply, attachments); filename != "" {
		return errors.New("finish mentions artifact filename " + filename + " without matching completionEvidence attachment")
	}
	return nil
}

func ValidateUserNoticeDelivery(notice string) error {
	if locator := finishMessageNonDeliverableArtifactLocator(notice); locator != "" {
		return errors.New("user_notice exposes non-deliverable artifact locator " + locator)
	}
	return nil
}

func FinishMessageContainsNonDeliverableArtifactLocator(reply string) bool {
	return finishMessageNonDeliverableArtifactLocator(reply) != ""
}

func finishMessageNonDeliverableArtifactLocator(reply string) string {
	normalizedReply := strings.TrimSpace(reply)
	for _, pattern := range finishMessageNonDeliverableLocatorPatterns() {
		if locator := strings.TrimSpace(regexp.MustCompile(pattern).FindString(normalizedReply)); locator != "" {
			return strings.Trim(locator, " ([")
		}
	}
	return ""
}

func finishMessageNonDeliverableLocatorPatterns() []string {
	return []string{
		`(?i)\bsandbox:/[^\s\])>"']+`,
		`(?i)\bfile:/+[^\s\])>"']+`,
		`(?i)(?:^|[\s\[(])/(?:mnt/data|tmp|root|workspace|var/folders)/[^\s\])>"']+`,
	}
}

func finishMessageUnattachedArtifactFilename(reply string, attachments []FileAttachment) string {
	attachedFilenames := attachedFilenameSet(attachments)
	for _, filename := range finishMessageArtifactFilenames(reply) {
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

func finishMessageArtifactFilenames(reply string) []string {
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
