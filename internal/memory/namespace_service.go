package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

const DefaultWorkspaceID = "default"

func UserNamespace(personID string) MemoryNamespace {
	return MemoryNamespace{
		NamespaceID:   "user:" + strings.TrimSpace(personID),
		ScopeType:     ScopeTypeUser,
		ScopePersonID: strings.TrimSpace(personID),
	}
}

func WorkspaceNamespace(workspaceID string, securityLevelRank int, requiredClasses []string) MemoryNamespace {
	cleanWorkspaceID := strings.TrimSpace(workspaceID)
	if cleanWorkspaceID == "" {
		cleanWorkspaceID = DefaultWorkspaceID
	}
	cleanClasses := normalizeClasses(requiredClasses)
	return MemoryNamespace{
		NamespaceID:       "workspace:" + cleanWorkspaceID + ":rank:" + strconv.Itoa(securityLevelRank) + ":classes:" + classHash(cleanClasses),
		ScopeType:         ScopeTypeWorkspace,
		SecurityLevelRank: securityLevelRank,
		RequiredClasses:   cleanClasses,
	}
}

func ConversationNamespace(conversationID string, securityLevelRank int, requiredClasses []string) MemoryNamespace {
	cleanClasses := normalizeClasses(requiredClasses)
	return MemoryNamespace{
		NamespaceID:         "conversation:" + strings.TrimSpace(conversationID) + ":rank:" + strconv.Itoa(securityLevelRank) + ":classes:" + classHash(cleanClasses),
		ScopeType:           ScopeTypeConversation,
		ScopeConversationID: strings.TrimSpace(conversationID),
		SecurityLevelRank:   securityLevelRank,
		RequiredClasses:     cleanClasses,
	}
}

func normalizeClasses(values []string) []string {
	uniqueValues := map[string]bool{}
	normalizedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || uniqueValues[trimmedValue] {
			continue
		}
		uniqueValues[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
	}
	sort.Strings(normalizedValues)
	return normalizedValues
}

func classHash(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	sum := sha256.Sum256([]byte(strings.Join(values, ",")))
	return hex.EncodeToString(sum[:])[:16]
}
