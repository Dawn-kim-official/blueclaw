package policy

import (
	"fmt"
	"testing"
)

func TestPolicyValidatorRejectsMoreThanTenCircles(t *testing.T) {
	policyDocument := PolicyDocument{
		Retention: RetentionPolicy{RawEventDays: 60},
		People: []PersonPolicy{
			{PersonID: "person-1", Emails: []string{"person@example.com"}},
		},
	}
	for index := 0; index < MaximumCircleCount+1; index++ {
		policyDocument.Circles = append(policyDocument.Circles, CirclePolicy{
			CircleID: fmt.Sprintf("circle-%d", index),
		})
	}

	errorValue := (PolicyValidator{}).ValidatePolicyDocument(policyDocument)

	if errorValue == nil {
		t.Fatal("expected circle limit validation error")
	}
}
