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
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1", "blueclaw", "bc_person_person-1", "2770") {
		t.Fatalf("expected private POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1/tmp", "blueclaw", "bc_person_person-1", "2770") {
		t.Fatalf("expected private tmp POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/private/people/person-1/artifacts", "blueclaw", "bc_person_person-1", "2770") {
		t.Fatalf("expected private artifacts POSIX directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles", "blueclaw", "blueclaw", "0711") {
		t.Fatalf("expected circles parent traversal directory, got %+v", state.Directories)
	}
	if !hasPOSIXDirectory(state, "/workspace/circles/finance", "blueclaw", "bc_circle_finance", "2770") {
		t.Fatalf("expected circle POSIX directory, got %+v", state.Directories)
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
