package enrollment

import (
	"encoding/json"
	"os"
	"strings"
)

type policyPerson struct {
	PersonID    string `json:"personID"`
	DisplayName string `json:"displayName"`
}

type policyDocument struct {
	People []policyPerson `json:"people"`
}

func OperatorPerson(home Home) (Person, bool) {
	policyBytes, errorValue := os.ReadFile(home.PolicyPath())
	if errorValue != nil {
		return Person{}, false
	}
	document := policyDocument{}
	if json.Unmarshal(policyBytes, &document) != nil || len(document.People) == 0 {
		return Person{}, false
	}
	firstPerson := document.People[0]
	if strings.TrimSpace(firstPerson.PersonID) == "" {
		return Person{}, false
	}
	return Person{PersonID: firstPerson.PersonID, DisplayName: firstPerson.DisplayName}, true
}
