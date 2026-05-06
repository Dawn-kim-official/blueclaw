package security

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
