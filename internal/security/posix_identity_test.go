package security

import (
	"testing"

	"blueclaw/internal/policy"
)

func TestLinuxIdentityNamesAreStableAndValid(t *testing.T) {
	if LinuxPersonUserName("person-1") != "bc_person_person-1" {
		t.Fatalf("unexpected person user name: %s", LinuxPersonUserName("Person One@example.com"))
	}
	if LinuxCircleGroupName("C-Level") != "bc_circle_c-level" {
		t.Fatalf("unexpected circle group name: %s", LinuxCircleGroupName("C-Level"))
	}
}

func TestLinuxIdentityNamesUseDeterministicSuffixForLongValues(t *testing.T) {
	name := LinuxPersonUserName("this-person-identifier-is-far-too-long-for-linux")
	if len(name) > posixNameMaximumLength {
		t.Fatalf("expected shortened name, got %q", name)
	}
	if name != LinuxPersonUserName("this-person-identifier-is-far-too-long-for-linux") {
		t.Fatal("expected deterministic shortened name")
	}
}

func TestLinuxIdentityNamesAvoidLossyNormalizationCollisions(t *testing.T) {
	firstName := LinuxPersonUserName("person!")
	secondName := LinuxPersonUserName("person?")
	if firstName == secondName {
		t.Fatalf("expected lossy normalized names to differ, got %q", firstName)
	}
	if firstName != LinuxPersonUserName("person!") {
		t.Fatal("expected deterministic lossy normalized name")
	}
}

func TestExecutionIdentityOmitsAdminGroupForRawTerminal(t *testing.T) {
	identity := ExecutionIdentityForPersonAccess(policy.PersonAccess{
		PersonID: "person-1",
		Circles:  []string{"staff", "admin"},
	}, "/workspace")

	if identity.UserName != "bc_person_person-1" {
		t.Fatalf("unexpected user name: %+v", identity)
	}
	for _, groupName := range identity.SupplementaryGroupNames {
		if groupName == "bc_circle_admin" {
			t.Fatalf("expected raw terminal identity to omit admin group, got %+v", identity.SupplementaryGroupNames)
		}
	}
}

func TestExecutionIdentityAddsStaffGroupForRequester(t *testing.T) {
	identity := ExecutionIdentityForPersonAccess(policy.PersonAccess{
		PersonID: "person-1",
	}, "/workspace")

	if !hasTestString(identity.SupplementaryGroupNames, "bc_circle_staff") {
		t.Fatalf("expected requester identity to include staff group, got %+v", identity.SupplementaryGroupNames)
	}
}

func TestPOSIXStateForPolicyProjectsWorkspaceDirectories(t *testing.T) {
	state := POSIXStateForPolicy(policy.PolicyDocument{
		People: []policy.PersonPolicy{{
			PersonID: "person-1",
			Circles:  []string{"finance"},
		}},
		Circles: []policy.CirclePolicy{{
			CircleID:               "finance",
			WorkspaceDirectoryPath: "/workspace/circles/finance",
		}},
	}, "/workspace")

	if !hasPOSIXDirectory(state, "/workspace/private", "blueclaw", "blueclaw", "0711") {
		t.Fatalf("expected private parent traversal directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people", "blueclaw", "blueclaw", "0711") {
		t.Fatalf("expected private people parent traversal directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1", "bc_person_person-1", "bc_person_person-1", "0700") {
		t.Fatalf("expected private POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1/tmp", "bc_person_person-1", "bc_person_person-1", "0700") {
		t.Fatalf("expected private tmp POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1/artifacts", "bc_person_person-1", "bc_person_person-1", "0700") {
		t.Fatalf("expected private artifacts POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles", "blueclaw", "blueclaw", "0711") {
		t.Fatalf("expected circles parent traversal directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles/staff", "blueclaw", "bc_circle_staff", "2770") {
		t.Fatalf("expected default staff circle POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles/staff/sites", "blueclaw", "bc_circle_staff", "2770") {
		t.Fatalf("expected staff site workspace directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles/finance", "blueclaw", "bc_circle_finance", "2770") {
		t.Fatalf("expected circle POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXGroup(state, "bc_circle_staff") {
		t.Fatalf("expected staff circle group, got %+v", state.Groups)
	}
	if !hasPOSIXUserGroup(state, "bc_person_person-1", "bc_circle_staff") {
		t.Fatalf("expected every requester POSIX user to be staff member, got %+v", state.Users)
	}
}

func TestPOSIXStateForPolicyGivesEveryPersonStaffAccess(t *testing.T) {
	state := POSIXStateForPolicy(policy.PolicyDocument{
		People: []policy.PersonPolicy{
			{PersonID: "person-1"},
			{PersonID: "person-2", Circles: []string{"finance"}},
			{PersonID: "admin-1", IsAdmin: true},
		},
	}, "/workspace")

	if !hasPOSIXDirectory(state, "/workspace/circles/staff", "blueclaw", "bc_circle_staff", "2770") {
		t.Fatalf("expected default staff circle directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles/staff/sites", "blueclaw", "bc_circle_staff", "2770") {
		t.Fatalf("expected default staff sites directory, got %+v", state.Directories)
	}
	if !hasPOSIXGroup(state, "bc_circle_staff") {
		t.Fatalf("expected staff circle group, got %+v", state.Groups)
	}
	for _, userName := range []string{"bc_person_person-1", "bc_person_person-2", "bc_person_admin-1"} {
		if !hasPOSIXUserGroup(state, userName, "bc_circle_staff") {
			t.Fatalf("expected %s to be a staff group member, got %+v", userName, state.Users)
		}
	}
}

func TestPOSIXStateForPolicyKeepsSharedRootReadOnlyButPublicAndCacheWritable(t *testing.T) {
	state := POSIXStateForPolicy(policy.PolicyDocument{
		People: []policy.PersonPolicy{{PersonID: "person-1"}},
	}, "/workspace")

	if !hasPOSIXDirectory(state, "/workspace/shared", "blueclaw", "bc_shared", "2755") {
		t.Fatalf("expected shared root to be group read-only (2755), got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/shared/public", "blueclaw", "bc_shared", "2775") {
		t.Fatalf("expected shared public to be group writable (2775), got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/shared/cache/dependencies", "blueclaw", "bc_shared", "2775") {
		t.Fatalf("expected dependency cache to be group writable (2775), got %+v", state.Directories)
	}
}

func hasPOSIXDirectory(state POSIXState, path string, owner string, group string, modeText string) bool {
	for _, directory := range state.Directories {
		if directory.Path == path && directory.Owner == owner && directory.Group == group && directory.ModeText == modeText {
			return true
		}
	}
	return false
}

func hasPOSIXGroup(state POSIXState, name string) bool {
	for _, group := range state.Groups {
		if group.Name == name {
			return true
		}
	}
	return false
}

func hasPOSIXUserGroup(state POSIXState, userName string, groupName string) bool {
	for _, user := range state.Users {
		if user.Name == userName && hasTestString(user.Groups, groupName) {
			return true
		}
	}
	return false
}

func hasTestString(values []string, expectedValue string) bool {
	for _, value := range values {
		if value == expectedValue {
			return true
		}
	}
	return false
}
