package enrollment

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func RegisterPerson(home Home, person Person) (bool, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(person.Email))
	if normalizedEmail == "" {
		return false, errors.New("a person needs an email, because that is how a message is matched to them")
	}
	policyBytes, errorValue := os.ReadFile(home.PolicyPath())
	if errorValue != nil {
		return false, errorValue
	}
	document := map[string]any{}
	if errorValue := json.Unmarshal(policyBytes, &document); errorValue != nil {
		return false, errorValue
	}
	people, _ := document["people"].([]any)
	for _, existingPerson := range people {
		if personEntry, isEntry := existingPerson.(map[string]any); isEntry && hasEmail(personEntry, normalizedEmail) {
			return false, nil
		}
	}
	personID := strings.TrimSpace(person.PersonID)
	if personID == "" {
		personID = newIdentifier()
	}
	document["people"] = append(people, map[string]any{
		"personID":          personID,
		"displayName":       strings.TrimSpace(person.DisplayName),
		"emails":            []string{normalizedEmail},
		"securityLevelName": "member",
		"securityLevelRank": 10,
		"circles":           []string{"staff"},
	})
	return true, writeJSONDocument(home.PolicyPath(), document)
}

func hasEmail(personEntry map[string]any, normalizedEmail string) bool {
	emails, _ := personEntry["emails"].([]any)
	for _, email := range emails {
		if emailText, isText := email.(string); isText && strings.ToLower(strings.TrimSpace(emailText)) == normalizedEmail {
			return true
		}
	}
	return false
}
