package security

import "blueclaw/internal/memory"

type AccessControlService struct{}

func (accessControlService AccessControlService) CanReadSecurityLabel(securityLevelRank int, grantedClasses []string, accessibleConversationIDs []string, securityLabel SecurityLabel) bool {
	if securityLevelRank < securityLabel.MinimumSecurityLevelRank {
		return false
	}
	if !containsAll(grantedClasses, securityLabel.RequiredClasses) {
		return false
	}
	if securityLabel.SourceConversationID == "" {
		return true
	}
	return contains(accessibleConversationIDs, securityLabel.SourceConversationID)
}

func (accessControlService AccessControlService) FilterAccessibleContentSegment(securityLevelRank int, grantedClasses []string, accessibleConversationIDs []string, contentSegments []memory.ContentSegment) []memory.ContentSegment {
	filteredContentSegments := []memory.ContentSegment{}

	for _, contentSegment := range contentSegments {
		securityLabel := SecurityLabel{
			SourceConversationID:     contentSegment.SourceConversationID,
			MinimumSecurityLevelRank: contentSegment.SecurityLevelRank,
			RequiredClasses:          contentSegment.RequiredClasses,
		}
		if accessControlService.CanReadSecurityLabel(securityLevelRank, grantedClasses, accessibleConversationIDs, securityLabel) {
			filteredContentSegments = append(filteredContentSegments, contentSegment)
		}
	}

	return filteredContentSegments
}

func (accessControlService AccessControlService) FilterAccessibleMemoryRecord(securityLevelRank int, grantedClasses []string, accessibleConversationIDs []string, memoryRecords []memory.MemoryRecord) []memory.MemoryRecord {
	filteredMemoryRecords := []memory.MemoryRecord{}

	for _, memoryRecord := range memoryRecords {
		securityLabel := SecurityLabel{
			SourceConversationID:     memoryRecord.SourceConversationID,
			MinimumSecurityLevelRank: memoryRecord.SecurityLevelRank,
			RequiredClasses:          memoryRecord.RequiredClasses,
		}
		if accessControlService.CanReadSecurityLabel(securityLevelRank, grantedClasses, accessibleConversationIDs, securityLabel) {
			filteredMemoryRecords = append(filteredMemoryRecords, memoryRecord)
		}
	}

	return filteredMemoryRecords
}

func containsAll(grantedClasses []string, requiredClasses []string) bool {
	grantedSet := map[string]bool{}
	for _, grantedClass := range grantedClasses {
		grantedSet[grantedClass] = true
	}
	for _, requiredClass := range requiredClasses {
		if !grantedSet[requiredClass] {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
