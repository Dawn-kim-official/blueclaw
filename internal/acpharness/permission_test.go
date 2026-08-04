package acpharness

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func selectedOptionID(t *testing.T, options []acp.PermissionOption) acp.PermissionOptionId {
	t.Helper()
	response, errorValue := (&sessionObserver{}).RequestPermission(context.Background(), acp.RequestPermissionRequest{Options: options})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Outcome.Selected == nil {
		t.Fatalf("expected an agent asking to act to be allowed, because POSIX permissions are the boundary, not this prompt; got %+v", response.Outcome)
	}
	return response.Outcome.Selected.OptionId
}

func TestAnAgentAskingToActIsAllowedBecauseTheKernelDecides(t *testing.T) {
	optionID := selectedOptionID(t, []acp.PermissionOption{
		{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce},
	})

	if optionID != "allow" {
		t.Fatalf("expected the allow option, got %q", optionID)
	}
}

func TestAllowAlwaysIsPreferredSoOneToolDoesNotAskEveryStep(t *testing.T) {
	optionID := selectedOptionID(t, []acp.PermissionOption{
		{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways},
	})

	if optionID != "always" {
		t.Fatalf("expected the allow-always option, got %q", optionID)
	}
}

func TestAnAgentOfferingNoWayToProceedIsCancelledRatherThanLeftWaiting(t *testing.T) {
	response, errorValue := (&sessionObserver{}).RequestPermission(context.Background(), acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce}},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Outcome.Cancelled == nil {
		t.Fatalf("expected a cancelled outcome when no option lets the turn proceed, got %+v", response.Outcome)
	}
}
