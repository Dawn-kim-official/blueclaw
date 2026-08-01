package bluecollar

import (
	"context"
	"testing"
)

// Regression guard for a device incident: a legitimate artifact task
// ("rebuild the IR deck") was blocked with "required evidence must name a
// registered native tool or capability operation" because file.read's
// Availability had been set to denied, which made IsAllowed report it as
// hidden. Kernel tools are always model-callable; the permission boundary is
// enforced at execution (POSIX/policy), never by hiding the tool from the
// model or from required-evidence validation.
func TestKernelToolsStayExposedEvenWhenAvailabilityDenied(t *testing.T) {
	deniedTool := func(name string) BoundTool {
		definition := testToolDescriptor(name)
		definition.Description = name
		return BoundTool{
			Definition:   definition,
			Availability: ToolAvailability{Status: ToolAvailabilityDenied, Reason: "policy"},
			Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
				return testToolSuccess("ok"), nil
			},
		}
	}

	toolSet := NewToolSet(append(append([]string{}, KernelToolNames()...), "domain.op"))
	for _, kernelToolName := range []string{FileReadToolName, FileDeliverToolName, TerminalRunToolName} {
		toolSet.RegisterBoundTool(deniedTool(kernelToolName))
		if !toolSet.IsAllowed(kernelToolName) {
			t.Fatalf("%s must stay exposed even with denied availability", kernelToolName)
		}
		if _, isValid := requiredEvidenceToolKind(toolSet, kernelToolName); !isValid {
			t.Fatalf("%s must be valid required evidence even with denied availability", kernelToolName)
		}
	}

	// Control: a non-kernel domain tool with denied availability stays hidden;
	// the always-exposed rule is scoped to kernel tools.
	toolSet.RegisterBoundTool(deniedTool("domain.op"))
	if toolSet.IsAllowed("domain.op") {
		t.Fatal("non-kernel tool with denied availability must stay hidden")
	}
}
