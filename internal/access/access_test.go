package access

import (
	"testing"

	"blueclaw/internal/policy"
)

func TestStaffCanAccessStaffCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}}
	resource := ResourceForWorkspacePath("/workspace", "/workspace/circles/staff/notes.md")

	if !CanAccess(Request{PersonAccess: personAccess, Action: ActionWrite, Resource: resource}) {
		t.Fatal("staff should write staff circle files")
	}
}

func TestCircleMemberCanAccessCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff", "finance"}}
	resource := ResourceForWorkspacePath("/workspace", "/workspace/circles/finance/report.xlsx")

	if !CanAccess(Request{PersonAccess: personAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("finance member should read finance circle files")
	}
}

func TestCircleNonMemberCannotAccessCircleFile(t *testing.T) {
	personAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}}
	resource := ResourceForWorkspacePath("/workspace", "/workspace/circles/finance/report.xlsx")

	if CanAccess(Request{PersonAccess: personAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("finance non-member should not read finance circle files")
	}
}

func TestPrivateFileOnlyAllowsOwner(t *testing.T) {
	ownerAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}}
	otherAccess := policy.PersonAccess{PersonID: "person-2", Circles: []string{"staff"}}
	resource := ResourceForWorkspacePath("/workspace", "/workspace/private/people/person-1/dm.md")

	if !CanAccess(Request{PersonAccess: ownerAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("private owner should read private files")
	}
	if CanAccess(Request{PersonAccess: otherAccess, Action: ActionRead, Resource: resource}) {
		t.Fatal("other person should not read private files")
	}
}

func TestRepresentativeToolPolicy(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:company.broadcast.send",
		Actions:  []string{ActionExecute},
		Circles:  []string{"representative"},
	}}
	representativeAccess := policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff", "representative"}, ResourceAccessRules: resourceAccessRules}
	staffAccess := policy.PersonAccess{PersonID: "person-2", Circles: []string{"staff"}, ResourceAccessRules: resourceAccessRules}

	if !CanAccess(Request{PersonAccess: representativeAccess, Action: ActionExecute, Resource: "tool:company.broadcast.send"}) {
		t.Fatal("representative should execute representative tool")
	}
	if CanAccess(Request{PersonAccess: staffAccess, Action: ActionExecute, Resource: "tool:company.broadcast.send"}) {
		t.Fatal("staff should not execute representative tool")
	}
}
