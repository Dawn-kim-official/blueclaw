package policy

import "testing"

func TestPolicyProjectionGivesStaffToEveryPerson(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		People: []PersonPolicy{{
			PersonID: "person-1",
			Emails:   []string{"person@example.com"},
		}},
	})

	personAccess := policyProjection.PersonAccessByPersonID["person-1"]
	if !hasTestPolicyString(personAccess.Circles, "staff") {
		t.Fatalf("expected staff circle, got %+v", personAccess.Circles)
	}
}

func TestPolicyProjectionAddsAdminWithoutGrantingCLevel(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		People: []PersonPolicy{{
			PersonID:       "admin-1",
			Emails:         []string{"admin@example.com"},
			IsAdmin:        true,
			GrantedClasses: []string{"internal", "executive"},
		}},
	})

	personAccess := policyProjection.PersonAccessByPersonID["admin-1"]
	if !hasTestPolicyString(personAccess.Circles, "staff") || !hasTestPolicyString(personAccess.Circles, "admin") {
		t.Fatalf("expected staff and admin circles, got %+v", personAccess.Circles)
	}
	if hasTestPolicyString(personAccess.Circles, "c-level") {
		t.Fatalf("expected executive legacy class not to grant c-level, got %+v", personAccess.Circles)
	}
}

func TestPolicyProjectionNormalizesExplicitCircles(t *testing.T) {
	policyProjection := PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(PolicyDocument{
		ResourceAccess: []ResourceAccessPolicy{{
			Resource: "tool:company.broadcast.send",
			Actions:  []string{"execute"},
			Circles:  []string{"representative"},
		}},
		People: []PersonPolicy{{
			PersonID: "person-1",
			Circles:  []string{" Staff ", "Finance", "finance"},
		}},
	})

	personAccess := policyProjection.PersonAccessByPersonID["person-1"]
	if len(personAccess.Circles) != 2 || personAccess.Circles[0] != "staff" || personAccess.Circles[1] != "finance" {
		t.Fatalf("expected normalized unique circles, got %+v", personAccess.Circles)
	}
	if len(personAccess.ResourceAccessRules) != 1 {
		t.Fatalf("expected resource access rules copied to person access, got %+v", personAccess.ResourceAccessRules)
	}
}

func hasTestPolicyString(values []string, expectedValue string) bool {
	for _, value := range values {
		if value == expectedValue {
			return true
		}
	}
	return false
}
