package policy

import (
	"encoding/json"
	"os"
)

type PolicyLoader struct{}

func (policyLoader PolicyLoader) LoadPolicyDocument(path string) (PolicyDocument, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return PolicyDocument{}, errorValue
	}

	var policyDocument PolicyDocument
	errorValue = json.Unmarshal(document, &policyDocument)
	if errorValue != nil {
		return PolicyDocument{}, errorValue
	}

	return policyDocument, nil
}
