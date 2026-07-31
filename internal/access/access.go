package access

import (
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

const (
	ActionRead    = "read"
	ActionWrite   = "write"
	ActionExecute = "execute"
	ActionManage  = "manage"
)

type Request struct {
	PersonAccess policy.PersonAccess
	Action       string
	Resource     string
}

func CanAccess(request Request) bool {
	resource := normalizeText(request.Resource)
	action := normalizeText(request.Action)
	if resource == "" || action == "" {
		return false
	}
	if len(matchingResourceRules(request.PersonAccess.ResourceAccessRules, action, resource)) > 0 {
		return canAccessPolicyResource(request.PersonAccess, action, resource)
	}
	if canAccessDerivedResource(request.PersonAccess, action, resource) {
		return true
	}
	if isDeniedDerivedResource(request.PersonAccess, resource) {
		return false
	}
	return canAccessPolicyResource(request.PersonAccess, action, resource)
}

func canAccessDerivedResource(personAccess policy.PersonAccess, action string, resource string) bool {
	if resource == "file:public" || resource == "file:workspace" || resource == "memory:workspace" || resource == "memory:conversation" {
		return action == ActionRead || action == ActionWrite
	}
	if circleID, isCircleResource := strings.CutPrefix(resource, "file:circle:"); isCircleResource {
		return actionAllowedForCircle(personAccess, action, circleID)
	}
	if circleID, isCircleResource := strings.CutPrefix(resource, "memory:circle:"); isCircleResource {
		return actionAllowedForCircle(personAccess, action, circleID)
	}
	if personID, isPrivateResource := strings.CutPrefix(resource, "file:private:"); isPrivateResource {
		return actionAllowedForPrivatePerson(personAccess, action, personID)
	}
	if personID, isPrivateResource := strings.CutPrefix(resource, "memory:private:"); isPrivateResource {
		return actionAllowedForPrivatePerson(personAccess, action, personID)
	}
	if resource == "file:internal" {
		return hasCircle(personAccess, "admin") && (action == ActionRead || action == ActionWrite || action == ActionManage)
	}
	return false
}

func isDeniedDerivedResource(personAccess policy.PersonAccess, resource string) bool {
	if circleID, isCircleResource := strings.CutPrefix(resource, "file:circle:"); isCircleResource {
		return !hasCircle(personAccess, circleID)
	}
	if circleID, isCircleResource := strings.CutPrefix(resource, "memory:circle:"); isCircleResource {
		return !hasCircle(personAccess, circleID)
	}
	if personID, isPrivateResource := strings.CutPrefix(resource, "file:private:"); isPrivateResource {
		return strings.TrimSpace(personAccess.PersonID) != strings.TrimSpace(personID)
	}
	if personID, isPrivateResource := strings.CutPrefix(resource, "memory:private:"); isPrivateResource {
		return strings.TrimSpace(personAccess.PersonID) != strings.TrimSpace(personID)
	}
	return resource == "file:internal" && !hasCircle(personAccess, "admin")
}

func canAccessPolicyResource(personAccess policy.PersonAccess, action string, resource string) bool {
	matchingRules := matchingResourceRules(personAccess.ResourceAccessRules, action, resource)
	if len(matchingRules) == 0 {
		return true
	}
	for _, rule := range matchingRules {
		if len(normalizeCircles(rule.Circles)) == 0 || hasAnyCircle(personAccess, rule.Circles) {
			return true
		}
	}
	return false
}

func matchingResourceRules(rules []policy.ResourceAccessPolicy, action string, resource string) []policy.ResourceAccessPolicy {
	matchingRules := []policy.ResourceAccessPolicy{}
	for _, rule := range rules {
		if normalizeText(rule.Resource) != resource {
			continue
		}
		if !containsAction(rule.Actions, action) {
			continue
		}
		matchingRules = append(matchingRules, rule)
	}
	return matchingRules
}

func containsAction(actions []string, action string) bool {
	for _, value := range actions {
		if normalizeText(value) == action {
			return true
		}
	}
	return false
}

func actionAllowedForCircle(personAccess policy.PersonAccess, action string, circleID string) bool {
	return (action == ActionRead || action == ActionWrite || action == ActionManage) && hasCircle(personAccess, circleID)
}

func actionAllowedForPrivatePerson(personAccess policy.PersonAccess, action string, personID string) bool {
	return (action == ActionRead || action == ActionWrite) && strings.TrimSpace(personAccess.PersonID) == strings.TrimSpace(personID)
}

func hasAnyCircle(personAccess policy.PersonAccess, circles []string) bool {
	for _, circleID := range normalizeCircles(circles) {
		if hasCircle(personAccess, circleID) {
			return true
		}
	}
	return false
}

func hasCircle(personAccess policy.PersonAccess, circleID string) bool {
	normalizedCircleID := normalizeText(circleID)
	for _, value := range normalizeCircles(personAccess.Circles) {
		if value == normalizedCircleID {
			return true
		}
	}
	return false
}

func normalizeCircles(values []string) []string {
	normalizedValues := []string{}
	seenValue := map[string]bool{}
	for _, value := range values {
		normalizedValue := normalizeText(value)
		if normalizedValue == "" || seenValue[normalizedValue] {
			continue
		}
		seenValue[normalizedValue] = true
		normalizedValues = append(normalizedValues, normalizedValue)
	}
	return normalizedValues
}

func normalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
